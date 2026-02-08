package state

// State holds the discovered runtime state of a kappal project.
type State struct {
	Project      string
	K3s          K3sInfo
	PortMap      map[string]int // "containerPort/proto" → hostPort (e.g. "80/tcp" → 8080)
	Services     map[string]*ServiceInfo
	K8sAvailable bool
	Kubeconfig   string // path to working kubeconfig
}

// K3sInfo holds Docker-level state of the K3s container.
type K3sInfo struct {
	ContainerName string
	ContainerID   string
	Status        string // "running", "stopped", "not found"
	Network       string
}

// ServiceInfo holds K8s-level state of a single compose service.
type ServiceInfo struct {
	Name     string
	Kind     string // "Deployment" or "Job"
	Image    string
	Status   string
	Replicas *Replicas // nil for Jobs
	Pods     []PodInfo
	Ports    []PortInfo
}

// Replicas holds Deployment replica counts.
type Replicas struct {
	Ready   int32
	Desired int32
}

// PodInfo holds state for a single K8s pod.
type PodInfo struct {
	Name   string
	Status string
	IP     string
}

// PortInfo holds a published port mapping.
type PortInfo struct {
	Host      int
	Container int
	Protocol  string
}
