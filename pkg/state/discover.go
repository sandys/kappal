package state

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kappal-app/kappal/pkg/docker"
	"github.com/kappal-app/kappal/pkg/k3s"
	"github.com/kappal-app/kappal/pkg/k8s"
)

// DiscoverOpts controls what data Discover fetches.
type DiscoverOpts struct {
	QueryK8s bool // if true, also queries K8s API for service/pod state
}

// Discover finds the live runtime state for a kappal project by querying
// Docker labels and (optionally) the K8s API. It never constructs names
// from conventions — everything comes from labels on live resources.
func Discover(ctx context.Context, projectName string, workspaceDir string, opts DiscoverOpts) (*State, error) {
	dockerClient, err := docker.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	defer func() { _ = dockerClient.Close() }()

	st := &State{
		Project:  projectName,
		PortMap:  make(map[string]int),
		Services: make(map[string]*ServiceInfo),
		K3s: K3sInfo{
			Status: "not found",
		},
	}

	// 1. Find K3s container by label
	containers, err := dockerClient.ContainerListByLabel(ctx, "kappal.io/project", projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to discover containers: %w", err)
	}

	var k3sContainer *docker.ContainerListEntry
	for i := range containers {
		st.K3s.ContainerName = containers[i].Name
		st.K3s.ContainerID = containers[i].ID
		st.K3s.Status = containers[i].Status
		k3sContainer = &containers[i]
		break // take the first match
	}

	if k3sContainer == nil {
		return st, nil
	}

	// 2. Find network by label
	networks, err := dockerClient.NetworkListByLabel(ctx, "kappal.io/project", projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to discover networks: %w", err)
	}
	if len(networks) > 0 {
		st.K3s.Network = networks[0]
	}

	if st.K3s.Status != "running" {
		return st, nil
	}

	// 3. Read Docker port bindings from K3s container
	portMap, err := dockerClient.ContainerInspectPorts(ctx, k3sContainer.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect K3s ports: %w", err)
	}
	for natPort, bindings := range portMap {
		containerPort := natPort.Int()
		proto := natPort.Proto()
		if containerPort == 6443 {
			continue // Skip K3s API port
		}
		if len(bindings) > 0 {
			if hp, err := strconv.Atoi(bindings[0].HostPort); err == nil {
				st.PortMap[fmt.Sprintf("%d/%s", containerPort, proto)] = hp
			}
		}
	}

	// 4. Ensure kubeconfig is reachable
	k3sManager, err := k3s.NewManager(workspaceDir, projectName)
	if err != nil {
		return st, nil // can't get kubeconfig but container state is populated
	}
	defer func() { _ = k3sManager.Close() }()

	if err := k3sManager.EnsureKubeconfig(ctx); err != nil {
		// Non-fatal — kubeconfig may not exist yet
		return st, nil
	}
	st.Kubeconfig = k3sManager.GetKubeconfigPath()

	if !opts.QueryK8s {
		return st, nil
	}

	// 5. Query K8s API
	st.K8sAvailable = queryK8sState(ctx, st)
	return st, nil
}

// queryK8sState fetches Deployment, Job, Service, and Pod data from K8s
// and populates st.Services. Returns true on success.
func queryK8sState(ctx context.Context, st *State) bool {
	k8sClient, err := k8s.NewClient(st.Kubeconfig)
	if err != nil {
		return false
	}

	labelSelector := fmt.Sprintf("kappal.io/project=%s", st.Project)

	deployments, err := k8sClient.ListDeployments(ctx, st.Project, labelSelector)
	if err != nil {
		return false
	}

	jobs, err := k8sClient.ListJobs(ctx, st.Project, labelSelector)
	if err != nil {
		return false
	}

	k8sServices, err := k8sClient.ListServices(ctx, st.Project, labelSelector)
	if err != nil {
		return false
	}

	pods, err := k8sClient.ListPods(ctx, st.Project, labelSelector)
	if err != nil {
		return false
	}

	// Build service port map: svcName → []{ port, protocol }
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

	// Build pods-by-service map
	podsByService := make(map[string][]PodInfo)
	for _, pod := range pods.Items {
		svcName := pod.Labels["kappal.io/service"]
		podsByService[svcName] = append(podsByService[svcName], PodInfo{
			Name:   pod.Name,
			Status: string(pod.Status.Phase),
			IP:     pod.Status.PodIP,
		})
	}

	// Populate deployments
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

		svcInfo := &ServiceInfo{
			Name:   svcName,
			Kind:   "Deployment",
			Image:  image,
			Status: status,
			Replicas: &Replicas{
				Ready:   ready,
				Desired: desired,
			},
		}

		// Filter to Running/Pending pods only for Deployments
		if svcPods := podsByService[svcName]; svcPods != nil {
			for _, p := range svcPods {
				if p.Status == "Running" || p.Status == "Pending" {
					svcInfo.Pods = append(svcInfo.Pods, p)
				}
			}
		}
		if svcInfo.Pods == nil {
			svcInfo.Pods = []PodInfo{}
		}

		// Correlate ports
		for _, sp := range svcPortMap[svcName] {
			key := fmt.Sprintf("%d/%s", sp.port, sp.protocol)
			if hostPort := st.PortMap[key]; hostPort > 0 {
				svcInfo.Ports = append(svcInfo.Ports, PortInfo{
					Host:      hostPort,
					Container: int(sp.port),
					Protocol:  sp.protocol,
				})
			}
		}

		st.Services[svcName] = svcInfo
	}

	// Populate jobs
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

		svcInfo := &ServiceInfo{
			Name:   svcName,
			Kind:   "Job",
			Image:  image,
			Status: status,
		}

		if svcPods := podsByService[svcName]; svcPods != nil {
			svcInfo.Pods = svcPods
		}
		if svcInfo.Pods == nil {
			svcInfo.Pods = []PodInfo{}
		}

		// Correlate ports
		for _, sp := range svcPortMap[svcName] {
			key := fmt.Sprintf("%d/%s", sp.port, sp.protocol)
			if hostPort := st.PortMap[key]; hostPort > 0 {
				svcInfo.Ports = append(svcInfo.Ports, PortInfo{
					Host:      hostPort,
					Container: int(sp.port),
					Protocol:  sp.protocol,
				})
			}
		}

		st.Services[svcName] = svcInfo
	}

	return true
}
