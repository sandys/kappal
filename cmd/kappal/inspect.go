package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kappal-app/kappal/pkg/compose"
	"github.com/kappal-app/kappal/pkg/docker"
	"github.com/kappal-app/kappal/pkg/k3s"
	"github.com/kappal-app/kappal/pkg/k8s"
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
	Schema   map[string]string   `json:"_schema"`
	Project  string              `json:"project"`
	K3s      inspectK3s          `json:"k3s"`
	Services []inspectService    `json:"services"`
}

// inspectSchema describes every field in the inspect JSON output.
// Embedded as _schema so the output is self-documenting for AI tools.
var inspectSchema = map[string]string{
	"project":                     "Compose project name, derived from directory name or -p flag. Also used as the K8s namespace.",
	"k3s.container":               "Docker container name running this project's K3s instance (format: kappal-<project>-k3s).",
	"k3s.status":                  "K3s container state. Values: 'running', 'stopped', 'not found'.",
	"k3s.network":                 "Docker bridge network isolating this project (format: kappal-<project>-net).",
	"services":                    "Array of services from the compose file (excluding profiled services). Each maps to a K8s Deployment or Job.",
	"services[].name":             "Service name from docker-compose.yaml. Used as K8s Deployment/Job name and DNS hostname.",
	"services[].kind":             "K8s workload type. When K8s is reachable, reflects actual cluster resource kind. When unavailable/missing, derived from compose restart policy. 'Deployment' for long-running, 'Job' for run-to-completion.",
	"services[].image":            "Container image running in this service. For locally-built images: '<project>-<service>:latest'.",
	"services[].status":           "Aggregated service health. Deployment values: 'running' (all replicas ready), 'waiting' (0 ready), 'partial' (some ready). Job values: 'completed' (succeeded), 'running' (active), 'failing' (active with prior failures), 'failed' (all failed), 'pending' (not started). Other: 'missing' (in compose but not in K8s), 'unavailable' (K8s API unreachable).",
	"services[].replicas":         "Replica counts for Deployments only. Omitted for Jobs.",
	"services[].replicas.ready":   "Number of pods that are running and passing readiness checks.",
	"services[].replicas.desired": "Target replica count from deploy.replicas in compose file (default 1).",
	"services[].ports":            "Published ports accessible from the host. Only present if compose file defines ports.",
	"services[].ports[].host":     "Port number on the Docker host. Use this for curl/HTTP requests from outside.",
	"services[].ports[].container": "Target port for the K8s Service and container (the compose 'target' value). Kappal sets both the K8s Service port and targetPort to this value.",
	"services[].ports[].protocol": "Transport protocol. Values: 'tcp', 'udp'.",
	"services[].pods":             "Individual pod instances for this service. For Deployments, only Running/Pending pods are shown. For Jobs, all pods (including Succeeded/Failed) are shown to reflect execution history.",
	"services[].pods[].name":      "K8s pod name (auto-generated, includes random suffix).",
	"services[].pods[].status":    "K8s pod phase. Deployment pods: 'Running', 'Pending'. Job pods: 'Running', 'Pending', 'Succeeded', 'Failed', 'Unknown'.",
	"services[].pods[].ip":        "Pod's cluster-internal IP address on the K3s overlay network.",
}

type inspectK3s struct {
	Container string `json:"container"`
	Status    string `json:"status"`
	Network   string `json:"network"`
}

type inspectService struct {
	Name     string           `json:"name"`
	Kind     string           `json:"kind"`
	Image    string           `json:"image"`
	Status   string           `json:"status"`
	Replicas *inspectReplicas `json:"replicas,omitempty"`
	Ports    []inspectPort    `json:"ports,omitempty"`
	Pods     []inspectPod     `json:"pods"`
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

type deploymentInfo struct {
	image    string
	status   string
	replicas *inspectReplicas
}

type jobInfo struct {
	image  string
	status string
}

type svcPort struct {
	port     int32
	protocol string
}

// queryK8sData fetches Deployment, Job, Service, and Pod data from the K8s API.
// It populates the provided maps and returns true on success.
// On any error (e.g. K8s API unreachable), it returns false without modifying the maps.
func queryK8sData(
	ctx context.Context,
	kubeconfigPath string,
	projectName string,
	deploymentMap map[string]deploymentInfo,
	jobMap map[string]jobInfo,
	svcPortMap map[string][]svcPort,
	podsByService map[string][]inspectPod,
) bool {
	k8sClient, err := k8s.NewClient(kubeconfigPath)
	if err != nil {
		return false
	}

	labelSelector := fmt.Sprintf("kappal.io/project=%s", projectName)

	deployments, err := k8sClient.ListDeployments(ctx, projectName, labelSelector)
	if err != nil {
		return false
	}

	jobs, err := k8sClient.ListJobs(ctx, projectName, labelSelector)
	if err != nil {
		return false
	}

	k8sServices, err := k8sClient.ListServices(ctx, projectName, labelSelector)
	if err != nil {
		return false
	}

	pods, err := k8sClient.ListPods(ctx, projectName, labelSelector)
	if err != nil {
		return false
	}

	// Populate deployment map
	for _, dep := range deployments.Items {
		svcName := dep.Labels["kappal.io/service"]
		image := ""
		if len(dep.Spec.Template.Spec.Containers) > 0 {
			image = dep.Spec.Template.Spec.Containers[0].Image
		}

		var desired int32
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}
		ready := dep.Status.ReadyReplicas

		status := "running"
		if ready == 0 {
			status = "waiting"
		} else if ready < desired {
			status = "partial"
		}

		deploymentMap[svcName] = deploymentInfo{
			image:  image,
			status: status,
			replicas: &inspectReplicas{
				Ready:   ready,
				Desired: desired,
			},
		}
	}

	// Populate job map
	for _, job := range jobs.Items {
		svcName := job.Labels["kappal.io/service"]
		image := ""
		if len(job.Spec.Template.Spec.Containers) > 0 {
			image = job.Spec.Template.Spec.Containers[0].Image
		}

		status := "pending"
		if job.Status.Succeeded > 0 {
			status = "completed"
		} else if job.Status.Active > 0 && job.Status.Failed > 0 {
			status = "failing"
		} else if job.Status.Active > 0 {
			status = "running"
		} else if job.Status.Failed > 0 {
			status = "failed"
		}

		jobMap[svcName] = jobInfo{
			image:  image,
			status: status,
		}
	}

	// Populate service port map
	for _, svc := range k8sServices.Items {
		for _, p := range svc.Spec.Ports {
			svcPortMap[svc.Name] = append(svcPortMap[svc.Name], svcPort{
				port:     p.Port,
				protocol: strings.ToLower(string(p.Protocol)),
			})
		}
	}

	// Populate pods-by-service map
	for _, pod := range pods.Items {
		svcName := pod.Labels["kappal.io/service"]
		podsByService[svcName] = append(podsByService[svcName], inspectPod{
			Name:   pod.Name,
			Status: string(pod.Status.Phase),
			IP:     pod.Status.PodIP,
		})
	}

	return true
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
	k3sManager, err := k3s.NewManager(workspaceDir, project.Name)
	if err != nil {
		return fmt.Errorf("failed to create K3s manager: %w", err)
	}
	defer func() { _ = k3sManager.Close() }()

	// Check K3s container state via Docker
	dockerClient, err := docker.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer func() { _ = dockerClient.Close() }()

	containerName := k3sManager.ContainerName()
	networkName := k3sManager.NetworkName()

	exists, running, err := dockerClient.ContainerState(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to check K3s container: %w", err)
	}

	k3sStatus := "not found"
	if exists && running {
		k3sStatus = "running"
	} else if exists {
		k3sStatus = "stopped"
	}

	result := inspectResult{
		Schema:  inspectSchema,
		Project: project.Name,
		K3s: inspectK3s{
			Container: containerName,
			Status:    k3sStatus,
			Network:   networkName,
		},
	}

	if k3sStatus != "running" {
		return outputJSON(result)
	}

	// Get Docker port bindings from K3s container
	dockerPorts, err := dockerClient.ContainerInspectPorts(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to inspect K3s ports: %w", err)
	}

	// Build container-port → host-port map from Docker bindings
	// Key: "containerPort/proto" (e.g. "80/tcp"), Value: host port
	portMap := make(map[string]int)
	for natPort, bindings := range dockerPorts {
		containerPort := natPort.Int()
		proto := natPort.Proto()
		if containerPort == 6443 {
			continue // Skip K3s API port
		}
		// Kappal creates exactly one binding per port (HostIP "0.0.0.0") — see buildExpectedPortBindings in k3s/manager.go
		if len(bindings) > 0 {
			if hp, err := strconv.Atoi(bindings[0].HostPort); err == nil {
				portMap[fmt.Sprintf("%d/%s", containerPort, proto)] = hp
			}
		}
	}

	// Ensure kubeconfig is reachable from this container (reconnects bridge network if needed)
	if err := k3sManager.EnsureKubeconfig(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	// Query K8s API — gracefully degrade if unreachable
	kubeconfigPath := k3sManager.GetKubeconfigPath()
	deploymentMap := make(map[string]deploymentInfo)
	jobMap := make(map[string]jobInfo)
	svcPortMap := make(map[string][]svcPort)
	podsByService := make(map[string][]inspectPod)
	k8sAvailable := queryK8sData(ctx, kubeconfigPath, project.Name, deploymentMap, jobMap, svcPortMap, podsByService)

	// Build services from compose file for deterministic, complete output
	serviceNames := make([]string, 0, len(project.Services))
	for name := range project.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	for _, name := range serviceNames {
		composeSvc := project.Services[name]

		// Skip services with profiles (not activated by default)
		if len(composeSvc.Profiles) > 0 {
			continue
		}

		composeKind := "Deployment"
		if composeSvc.Restart == "no" {
			composeKind = "Job"
		}

		svc := inspectService{
			Name: name,
			Pods: []inspectPod{},
		}

		if !k8sAvailable {
			svc.Kind = composeKind
			svc.Image = composeSvc.Image
			svc.Status = "unavailable"
		} else if di, ok := deploymentMap[name]; ok {
			// Deployment checked first: if both exist (stale drift), Deployment wins.
			// This is safe because up.go deletes all Jobs before re-applying manifests.
			svc.Kind = "Deployment"
			svc.Image = di.image
			svc.Status = di.status
			svc.Replicas = di.replicas
			// Filter to Running/Pending pods only — completed/failed pods from
			// previous rollouts accumulate because K8s has no TTL on them.
			if pods := podsByService[name]; pods != nil {
				for _, p := range pods {
					if p.Status == "Running" || p.Status == "Pending" {
						svc.Pods = append(svc.Pods, p)
					}
				}
			}
			// Correlate ports
			for _, sp := range svcPortMap[name] {
				key := fmt.Sprintf("%d/%s", sp.port, sp.protocol)
				hostPort := portMap[key]
				if hostPort > 0 {
					svc.Ports = append(svc.Ports, inspectPort{
						Host:      hostPort,
						Container: int(sp.port),
						Protocol:  sp.protocol,
					})
				}
			}
		} else if ji, ok := jobMap[name]; ok {
			svc.Kind = "Job"
			svc.Image = ji.image
			svc.Status = ji.status
			if pods := podsByService[name]; pods != nil {
				svc.Pods = pods
			}
			// Correlate ports
			for _, sp := range svcPortMap[name] {
				key := fmt.Sprintf("%d/%s", sp.port, sp.protocol)
				hostPort := portMap[key]
				if hostPort > 0 {
					svc.Ports = append(svc.Ports, inspectPort{
						Host:      hostPort,
						Container: int(sp.port),
						Protocol:  sp.protocol,
					})
				}
			}
		} else {
			svc.Kind = composeKind
			svc.Image = composeSvc.Image
			svc.Status = "missing"
		}

		result.Services = append(result.Services, svc)
	}

	if result.Services == nil {
		result.Services = []inspectService{}
	}

	return outputJSON(result)
}

func outputJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
