package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
Outputs JSON with services, ports, replicas, pod status, and more.
All data comes from live K8s and Docker APIs, not from compose files.`,
	RunE: runInspect,
}

// inspectOutput types for JSON serialization
type inspectResult struct {
	Project  string              `json:"project"`
	K3s      inspectK3s          `json:"k3s"`
	Services []inspectService    `json:"services"`
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

	project, err := compose.Load(composePath, projectName)
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
		if len(bindings) > 0 {
			if hp, err := strconv.Atoi(bindings[0].HostPort); err == nil {
				portMap[fmt.Sprintf("%d/%s", containerPort, proto)] = hp
			}
		}
	}

	// Query K8s API
	kubeconfigPath := k3sManager.GetKubeconfigPath()
	k8sClient, err := k8s.NewClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to create k8s client: %w", err)
	}

	labelSelector := fmt.Sprintf("kappal.io/project=%s", project.Name)

	// Query deployments
	deployments, err := k8sClient.ListDeployments(ctx, project.Name, labelSelector)
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	// Query jobs
	jobs, err := k8sClient.ListJobs(ctx, project.Name, labelSelector)
	if err != nil {
		return fmt.Errorf("failed to list jobs: %w", err)
	}

	// Query services for port info
	k8sServices, err := k8sClient.ListServices(ctx, project.Name, labelSelector)
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	// Build service name → K8s Service port map
	type svcPort struct {
		port     int32
		protocol string
	}
	svcPortMap := make(map[string][]svcPort)
	for _, svc := range k8sServices.Items {
		for _, p := range svc.Spec.Ports {
			svcPortMap[svc.Name] = append(svcPortMap[svc.Name], svcPort{
				port:     p.Port,
				protocol: strings.ToLower(string(p.Protocol)),
			})
		}
	}

	// Query all pods
	pods, err := k8sClient.ListPods(ctx, project.Name, labelSelector)
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	// Build service name → pods map
	podsByService := make(map[string][]inspectPod)
	for _, pod := range pods.Items {
		svcName := pod.Labels["kappal.io/service"]
		podsByService[svcName] = append(podsByService[svcName], inspectPod{
			Name:   pod.Name,
			Status: string(pod.Status.Phase),
			IP:     pod.Status.PodIP,
		})
	}

	// Build inspect services from deployments
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

		svc := inspectService{
			Name:   svcName,
			Kind:   "Deployment",
			Image:  image,
			Status: status,
			Replicas: &inspectReplicas{
				Ready:   ready,
				Desired: desired,
			},
			Pods: podsByService[svcName],
		}

		// Correlate ports
		for _, sp := range svcPortMap[svcName] {
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

		if svc.Pods == nil {
			svc.Pods = []inspectPod{}
		}

		result.Services = append(result.Services, svc)
	}

	// Build inspect services from jobs
	for _, job := range jobs.Items {
		svcName := job.Labels["kappal.io/service"]
		image := ""
		if len(job.Spec.Template.Spec.Containers) > 0 {
			image = job.Spec.Template.Spec.Containers[0].Image
		}

		status := "pending"
		if job.Status.Succeeded > 0 {
			status = "completed"
		} else if job.Status.Active > 0 {
			status = "running"
		} else if job.Status.Failed > 0 {
			status = "failed"
		}

		svc := inspectService{
			Name:   svcName,
			Kind:   "Job",
			Image:  image,
			Status: status,
			Pods:   podsByService[svcName],
		}

		if svc.Pods == nil {
			svc.Pods = []inspectPod{}
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
