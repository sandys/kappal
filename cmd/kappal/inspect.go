package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kappal-app/kappal/pkg/compose"
	"github.com/kappal-app/kappal/pkg/state"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Show detailed project state as JSON",
	Long: `Query K3s and Kubernetes APIs to show the current state of a project.
Outputs a single JSON object to stdout with services, ports, replicas, pod status,
and K3s container info. Combines compose file (for service definitions) with live
K8s and Docker APIs (for runtime state).

The JSON includes a top-level "_schema" field describing every data field, making
the output self-documenting for AI tools and automation.

Output structure:
  _schema        Map of field path → human-readable description
  project        Compose project name (used as K8s namespace)
  k3s            K3s container state: container name, status, network
  services[]     Array of services with kind, image, status, replicas, ports, pods

If K3s is not running, services will be an empty array. If K3s is running but the
API is unreachable, services are listed from the compose file with status "unavailable".

Flags:
  -f <path>      Compose file path (default: docker-compose.yaml)
  -p <name>      Override project name

Examples:
  kappal inspect                          Full project state
  kappal inspect | jq '.services[].name'  List service names
  kappal inspect | jq '.services[] | select(.status=="running") | .ports[].host'
                                          Get host ports of running services
  kappal inspect | jq '.k3s.status'       Check if K3s is running`,
	RunE: runInspect,
}

// inspectOutput types for JSON serialization
type inspectResult struct {
	Schema   map[string]string `json:"_schema"`
	Project  string            `json:"project"`
	K3s      inspectK3s        `json:"k3s"`
	Services []inspectService  `json:"services"`
}

// inspectSchema describes every field in the inspect JSON output.
// Embedded as _schema so the output is self-documenting for AI tools.
var inspectSchema = map[string]string{
	"project":                      "Compose project name, derived from directory name or -p flag. Also used as the K8s namespace.",
	"k3s.container":                "Docker container name running this project's K3s instance (format: kappal-<project>-k3s).",
	"k3s.status":                   "K3s container state. Values: 'running', 'stopped', 'not found'.",
	"k3s.network":                  "Docker bridge network isolating this project (format: kappal-<project>-net).",
	"services":                     "Array of services from the compose file (excluding profiled services). Each maps to a K8s Deployment or Job.",
	"services[].name":              "Service name from docker-compose.yaml. Used as K8s Deployment/Job name and DNS hostname.",
	"services[].kind":              "K8s workload type. When K8s is reachable, reflects actual cluster resource kind. When unavailable/missing, derived from compose restart policy. 'Deployment' for long-running, 'Job' for run-to-completion.",
	"services[].image":             "Container image running in this service. For locally-built images: '<project>-<service>:latest'.",
	"services[].status":            "Aggregated service health. Deployment values: 'running' (all replicas ready), 'waiting' (0 ready), 'partial' (some ready). Job values: 'completed' (succeeded), 'running' (active), 'failing' (active with prior failures), 'failed' (all failed), 'pending' (not started). Other: 'missing' (in compose but not in K8s), 'unavailable' (K8s API unreachable).",
	"services[].replicas":          "Replica counts for Deployments only. Omitted for Jobs.",
	"services[].replicas.ready":    "Number of pods that are running and passing readiness checks.",
	"services[].replicas.desired":  "Target replica count from deploy.replicas in compose file (default 1).",
	"services[].ports":             "Published ports accessible from the host. Only present if compose file defines ports.",
	"services[].ports[].host":      "Port number on the Docker host. Use this for curl/HTTP requests from outside.",
	"services[].ports[].container": "Target port for the K8s Service and container (the compose 'target' value). Kappal sets both the K8s Service port and targetPort to this value.",
	"services[].ports[].protocol":  "Transport protocol. Values: 'tcp', 'udp'.",
	"services[].healthcheck":              "Compose healthcheck definition, mapped to a K8s readiness probe. Only present if the compose service defines a healthcheck.",
	"services[].healthcheck.test":         "Healthcheck command. Format: ['CMD-SHELL', 'command'] or ['CMD', 'arg1', ...'].",
	"services[].healthcheck.interval":     "Time between probe attempts (e.g. '10s'). Maps to K8s readinessProbe.periodSeconds.",
	"services[].healthcheck.timeout":      "Max time for a single probe (e.g. '5s'). Maps to K8s readinessProbe.timeoutSeconds.",
	"services[].healthcheck.retries":      "Consecutive failures before marking unhealthy. Maps to K8s readinessProbe.failureThreshold.",
	"services[].healthcheck.start_period": "Grace period before probes count (e.g. '30s'). Maps to K8s readinessProbe.initialDelaySeconds.",
	"services[].pods":                     "Individual pod instances for this service. For Deployments, only Running/Pending pods are shown. For Jobs, all pods (including Succeeded/Failed) are shown to reflect execution history.",
	"services[].pods[].name":              "K8s pod name (auto-generated, includes random suffix).",
	"services[].pods[].status":            "K8s pod phase. Deployment pods: 'Running', 'Pending'. Job pods: 'Running', 'Pending', 'Succeeded', 'Failed', 'Unknown'.",
	"services[].pods[].ip":                "Pod's cluster-internal IP address on the K3s overlay network.",
}

type inspectK3s struct {
	Container string `json:"container"`
	Status    string `json:"status"`
	Network   string `json:"network"`
}

type inspectService struct {
	Name        string              `json:"name"`
	Kind        string              `json:"kind"`
	Image       string              `json:"image"`
	Status      string              `json:"status"`
	Replicas    *inspectReplicas    `json:"replicas,omitempty"`
	Ports       []inspectPort       `json:"ports,omitempty"`
	HealthCheck *inspectHealthCheck `json:"healthcheck,omitempty"`
	Pods        []inspectPod        `json:"pods"`
}

type inspectHealthCheck struct {
	Test        []string `json:"test"`
	Interval    string   `json:"interval,omitempty"`
	Timeout     string   `json:"timeout,omitempty"`
	Retries     int      `json:"retries,omitempty"`
	StartPeriod string   `json:"start_period,omitempty"`
}

type inspectReplicas struct {
	Ready   int32 `json:"ready"`
	Desired int32 `json:"desired"`
}

type inspectPort struct {
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol"`
}

type inspectPod struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	IP     string `json:"ip"`
}

func runInspect(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	composePath := composeFile
	if !filepath.IsAbs(composePath) {
		composePath = filepath.Join(projectDir, composePath)
	}

	resolvedName := resolveProjectName(projectName, filepath.Dir(composePath))
	project, err := compose.Load(composePath, resolvedName)
	if err != nil {
		return fmt.Errorf("failed to load compose file: %w", err)
	}

	workspaceDir := filepath.Join(projectDir, ".kappal")

	// Discover live state via labels
	discovered, err := state.Discover(ctx, project.Name, workspaceDir, state.DiscoverOpts{QueryK8s: true})
	if err != nil {
		return fmt.Errorf("failed to discover state: %w", err)
	}

	result := inspectResult{
		Schema:  inspectSchema,
		Project: project.Name,
		K3s: inspectK3s{
			Container: discovered.K3s.ContainerName,
			Status:    discovered.K3s.Status,
			Network:   discovered.K3s.Network,
		},
	}

	if discovered.K3s.Status != "running" {
		result.Services = []inspectService{}
		return outputJSON(result)
	}

	// Merge compose definitions with discovered K8s state
	merged := state.MergeCompose(discovered, project)
	for _, svc := range merged {
		iSvc := inspectService{
			Name:   svc.Name,
			Kind:   svc.Kind,
			Image:  svc.Image,
			Status: svc.Status,
			Pods:   convertPods(svc.Pods),
		}
		if svc.Replicas != nil {
			iSvc.Replicas = &inspectReplicas{
				Ready:   svc.Replicas.Ready,
				Desired: svc.Replicas.Desired,
			}
		}
		for _, p := range svc.Ports {
			iSvc.Ports = append(iSvc.Ports, inspectPort{
				Host:      p.Host,
				Container: p.Container,
				Protocol:  p.Protocol,
			})
		}
		if svc.HealthCheck != nil {
			iSvc.HealthCheck = &inspectHealthCheck{
				Test:        svc.HealthCheck.Test,
				Interval:    svc.HealthCheck.Interval,
				Timeout:     svc.HealthCheck.Timeout,
				Retries:     svc.HealthCheck.Retries,
				StartPeriod: svc.HealthCheck.StartPeriod,
			}
		}
		result.Services = append(result.Services, iSvc)
	}

	if result.Services == nil {
		result.Services = []inspectService{}
	}

	return outputJSON(result)
}

// convertPods converts state.PodInfo to the inspect-specific inspectPod type.
func convertPods(pods []state.PodInfo) []inspectPod {
	result := make([]inspectPod, len(pods))
	for i, p := range pods {
		result[i] = inspectPod{Name: p.Name, Status: p.Status, IP: p.IP}
	}
	return result
}

func outputJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
