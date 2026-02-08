package k3s

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"github.com/kappal-app/kappal/pkg/docker"
	"github.com/kappal-app/kappal/pkg/k8s"
)

const (
	K3sImage = "docker.io/rancher/k3s:v1.29.0-k3s1"
)

// PublishedPort represents a port to publish from the K3s container to the host.
type PublishedPort struct {
	HostPort      uint32
	ContainerPort uint32
	Protocol      string // "tcp" or "udp"
}

// Manager handles K3s lifecycle only (Docker start/stop)
// All Kubernetes operations go through client-go after K3s is running
type Manager struct {
	workspaceDir   string
	runtimeDir     string
	projectName    string
	publishedPorts []PublishedPort
	docker         *docker.Client
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// sanitize replaces invalid Docker name characters with dashes.
func sanitize(name string) string {
	return sanitizeRe.ReplaceAllString(name, "-")
}

// NewManager creates a new K3s manager for the given project.
func NewManager(workspaceDir string, projectName string) (*Manager, error) {
	dockerClient, err := docker.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	return &Manager{
		workspaceDir: workspaceDir,
		runtimeDir:   filepath.Join(workspaceDir, "runtime"),
		projectName:  projectName,
		docker:       dockerClient,
	}, nil
}

// SetPublishedPorts sets the compose service ports to publish on the K3s container.
// Must be called before EnsureRunning. Returns an error if duplicate container
// port/protocol combinations are found.
func (m *Manager) SetPublishedPorts(ports []PublishedPort) error {
	seen := make(map[string]bool)
	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		key := fmt.Sprintf("%d/%s", p.ContainerPort, proto)
		if seen[key] {
			return fmt.Errorf("two services publish container port %d/%s — each container port can only be used by one service", p.ContainerPort, proto)
		}
		seen[key] = true
	}
	m.publishedPorts = ports
	return nil
}

// containerName returns the Docker container name for this project's K3s instance.
func (m *Manager) containerName() string {
	return fmt.Sprintf("kappal-%s-k3s", sanitize(m.projectName))
}

// ContainerName returns the Docker container name (exported for inspect).
func (m *Manager) ContainerName() string {
	return m.containerName()
}

// networkName returns the Docker bridge network name for this project.
func (m *Manager) networkName() string {
	return fmt.Sprintf("kappal-%s-net", sanitize(m.projectName))
}

// NetworkName returns the Docker bridge network name (exported for inspect).
func (m *Manager) NetworkName() string {
	return m.networkName()
}

// apiHostPort returns a deterministic host port for the K3s API server,
// derived from the project name. Range: 16443–26442.
func (m *Manager) apiHostPort() uint32 {
	h := sha256.Sum256([]byte(m.projectName))
	offset := binary.BigEndian.Uint16(h[:2]) % 10000
	return 16443 + uint32(offset)
}

// Close closes the Docker client
func (m *Manager) Close() error {
	if m.docker != nil {
		return m.docker.Close()
	}
	return nil
}

// getVolumeNamePrefix returns a unique prefix for named Docker volumes based on project name.
// This ensures volumes are isolated per project.
func (m *Manager) getVolumeNamePrefix() string {
	hash := sha256.Sum256([]byte(m.projectName))
	return "kappal-" + hex.EncodeToString(hash[:8])
}

// getK3sDataVolumeName returns the Docker volume name for K3s data
func (m *Manager) getK3sDataVolumeName() string {
	return m.getVolumeNamePrefix() + "-k3s-data"
}

// GetKubeconfigPath returns the path to the kubeconfig file
func (m *Manager) GetKubeconfigPath() string {
	return filepath.Join(m.runtimeDir, "kubeconfig.yaml")
}

// GetRuntimeDir returns the runtime directory path
func (m *Manager) GetRuntimeDir() string {
	return m.runtimeDir
}

// buildExpectedPortBindings constructs the expected nat.PortMap for the current config.
func (m *Manager) buildExpectedPortBindings() nat.PortMap {
	portBindings := nat.PortMap{}

	// API port
	apiPort, _ := nat.NewPort("tcp", "6443")
	portBindings[apiPort] = []nat.PortBinding{
		{HostIP: "0.0.0.0", HostPort: fmt.Sprintf("%d", m.apiHostPort())},
	}

	// Compose published ports
	for _, p := range m.publishedPorts {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		containerPort, _ := nat.NewPort(proto, fmt.Sprintf("%d", p.ContainerPort))
		portBindings[containerPort] = []nat.PortBinding{
			{HostIP: "0.0.0.0", HostPort: fmt.Sprintf("%d", p.HostPort)},
		}
	}

	return portBindings
}

// portBindingsMatch checks if the running container's ports match the expected config.
func portBindingsMatch(running, expected nat.PortMap) bool {
	if len(running) != len(expected) {
		return false
	}
	for port, expectedBindings := range expected {
		runningBindings, ok := running[port]
		if !ok || len(runningBindings) != len(expectedBindings) {
			return false
		}
		for i, eb := range expectedBindings {
			if runningBindings[i].HostPort != eb.HostPort {
				return false
			}
		}
	}
	return true
}

// EnsureRunning starts K3s if not already running.
// If the container is running but port bindings have changed, it recreates K3s.
func (m *Manager) EnsureRunning(ctx context.Context) error {
	containerName := m.containerName()

	exists, running, err := m.docker.ContainerState(ctx, containerName)
	if err != nil {
		return err
	}

	if running {
		// Check for port binding mismatch
		currentPorts, err := m.docker.ContainerInspectPorts(ctx, containerName)
		if err != nil {
			return fmt.Errorf("failed to inspect container ports: %w", err)
		}

		expectedPorts := m.buildExpectedPortBindings()
		if !portBindingsMatch(currentPorts, expectedPorts) {
			fmt.Println("Port config changed, recreating K3s...")
			if err := m.docker.ContainerStop(ctx, containerName, 10*time.Second); err != nil {
				return fmt.Errorf("failed to stop K3s: %w", err)
			}
			if err := m.docker.ContainerRemove(ctx, containerName); err != nil {
				return fmt.Errorf("failed to remove K3s: %w", err)
			}
			return m.start(ctx)
		}

		fmt.Println("K3s already running")
		return m.waitForReady(ctx)
	}

	// Remove any existing stopped/dead container before starting fresh
	if exists {
		if err := m.docker.ContainerRemove(ctx, containerName); err != nil {
			return fmt.Errorf("failed to remove existing container: %w", err)
		}
	}

	// Create and start new container
	return m.start(ctx)
}

// checkPortAvailability verifies all required ports are free on the host.
func (m *Manager) checkPortAvailability() error {
	// Check API port
	apiPort := m.apiHostPort()
	if err := checkTCPPort(apiPort); err != nil {
		return fmt.Errorf("FATAL: K3s API port %d is already in use.\n"+
			"This port is auto-assigned from your project name. Another kappal project has\n"+
			"the same assignment. Use -p <different-name> to pick a different project name", apiPort)
	}

	// Check compose published ports
	for _, p := range m.publishedPorts {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		switch proto {
		case "tcp":
			if err := checkTCPPort(p.HostPort); err != nil {
				return fmt.Errorf("FATAL: Port %d/tcp is already in use.\n"+
					"Another service is already listening on this port. Change the published port\n"+
					"in your docker-compose.yaml, or stop the conflicting service", p.HostPort)
			}
		case "udp":
			if err := checkUDPPort(p.HostPort); err != nil {
				return fmt.Errorf("FATAL: Port %d/udp is already in use.\n"+
					"Another service is already listening on this port. Change the published port\n"+
					"in your docker-compose.yaml, or stop the conflicting service", p.HostPort)
			}
		}
	}

	return nil
}

func checkTCPPort(port uint32) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	_ = ln.Close()
	return nil
}

func checkUDPPort(port uint32) error {
	ln, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	_ = ln.Close()
	return nil
}

func (m *Manager) start(ctx context.Context) error {
	fmt.Println("Starting K3s...")

	// Create runtime directory if it doesn't exist
	if err := os.MkdirAll(m.runtimeDir, 0755); err != nil {
		return fmt.Errorf("failed to create runtime directory: %w", err)
	}

	// Create bridge network for isolation (with project label for discovery)
	networkLabels := map[string]string{
		"kappal.io/project": m.projectName,
	}
	if err := m.docker.NetworkCreateWithLabels(ctx, m.networkName(), networkLabels); err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}

	// Check port availability before starting
	if err := m.checkPortAvailability(); err != nil {
		return err
	}

	// Use a named Docker volume for K3s data persistence.
	k3sDataVolume := m.getK3sDataVolumeName()

	// Build port bindings
	portBindings := m.buildExpectedPortBindings()

	// Build exposed ports set for container config
	exposedPorts := nat.PortSet{}
	for port := range portBindings {
		exposedPorts[port] = struct{}{}
	}

	// Build container config
	config := &container.Config{
		Hostname: m.containerName(), // Stable hostname ensures K3s node name persists across container recreation
		Image:    K3sImage,
		Cmd: []string{
			"server",
			"--disable=traefik",
			"--disable=metrics-server",
			"--bind-address=0.0.0.0",
			"--tls-san=0.0.0.0",
			"--tls-san=127.0.0.1",
		},
		Env: []string{
			"K3S_KUBECONFIG_MODE=644",
		},
		ExposedPorts: exposedPorts,
		Labels: map[string]string{
			"kappal.io/project": m.projectName,
			"kappal.io/role":    "k3s",
		},
	}

	// Build host config with privileged mode and bridge networking
	hostConfig := &container.HostConfig{
		Privileged:    true,
		NetworkMode:   container.NetworkMode(m.networkName()),
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		PortBindings:  portBindings,
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: k3sDataVolume,
				Target: "/var/lib/rancher/k3s",
			},
		},
	}

	if err := m.docker.ContainerRunWithNetwork(ctx, config, hostConfig, m.networkName(), m.containerName()); err != nil {
		return fmt.Errorf("failed to start K3s: %w", err)
	}

	return m.waitForReady(ctx)
}

// isInsideDocker returns true if the current process is running inside a Docker container.
func isInsideDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// getSelfContainerID returns the container ID of the current process if running in Docker.
func getSelfContainerID() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	// Docker sets hostname to the short container ID (12 hex chars)
	if len(hostname) >= 12 {
		if _, err := hex.DecodeString(hostname[:12]); err == nil {
			return hostname
		}
	}
	return ""
}

// serverURLRegexp matches the server URL in a kubeconfig file.
var serverURLRegexp = regexp.MustCompile(`(server:\s*)https://[^:\s]+:\d+`)

// replaceServerURL replaces the server URL in kubeconfig content with the target URL.
func replaceServerURL(kubeconfig, target string) string {
	return serverURLRegexp.ReplaceAllString(kubeconfig, "${1}"+target)
}

// resolveAPIEndpoint determines the correct API server host and port for the
// current execution context, and connects to the bridge network if running in Docker.
func (m *Manager) resolveAPIEndpoint(ctx context.Context) (host string, port uint32) {
	host = "127.0.0.1"
	port = m.apiHostPort()

	if isInsideDocker() {
		selfID := getSelfContainerID()
		if selfID != "" {
			// Connect our container to the K3s bridge network so we can reach K3s directly
			if err := m.docker.NetworkConnect(ctx, m.networkName(), selfID); err != nil {
				fmt.Fprintf(os.Stderr, "\nWarning: could not connect to K3s network: %v\n", err)
			} else {
				// Get K3s container's IP on the bridge network
				ip, err := m.docker.ContainerIPOnNetwork(ctx, m.containerName(), m.networkName())
				if err == nil && ip != "" {
					host = ip
					port = 6443
				}
			}
		}
	}
	return
}

// EnsureKubeconfig ensures the kubeconfig is reachable from the current
// execution context. When running inside Docker, this connects the current
// container to the project's bridge network and re-patches the kubeconfig
// if the server address has changed.
func (m *Manager) EnsureKubeconfig(ctx context.Context) error {
	kubeconfigPath := m.GetKubeconfigPath()
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return nil // no kubeconfig yet, nothing to do
	}

	host, port := m.resolveAPIEndpoint(ctx)
	target := fmt.Sprintf("https://%s:%d", host, port)

	data, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return err
	}

	patched := replaceServerURL(string(data), target)
	if patched != string(data) {
		return os.WriteFile(kubeconfigPath, []byte(patched), 0644)
	}
	return nil
}

// waitForReady waits for K3s to be ready and extracts the kubeconfig
func (m *Manager) waitForReady(ctx context.Context) error {
	fmt.Print("Waiting for K3s to be ready")

	// Ensure runtime directory exists
	if err := os.MkdirAll(m.runtimeDir, 0755); err != nil {
		return fmt.Errorf("failed to create runtime directory: %w", err)
	}

	containerName := m.containerName()

	// Determine the API server address based on execution context
	host, port := m.resolveAPIEndpoint(ctx)

	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		// Extract kubeconfig using docker exec cat (more reliable than docker cp -)
		output, err := m.docker.ContainerExec(ctx, containerName, []string{"cat", "/etc/rancher/k3s/k3s.yaml"})
		if err == nil && len(output) > 0 {
			// Patch kubeconfig to use the correct API server address
			replacement := fmt.Sprintf("https://%s:%d", host, port)
			patched := replaceServerURL(string(output), replacement)

			// Write kubeconfig to runtime directory
			kubeconfigPath := m.GetKubeconfigPath()
			if err := os.WriteFile(kubeconfigPath, []byte(patched), 0644); err != nil {
				return fmt.Errorf("failed to write kubeconfig: %w", err)
			}

			// Test connection via client-go
			client, err := k8s.NewClient(kubeconfigPath)
			if err == nil {
				if err := client.CheckConnection(ctx); err == nil {
					fmt.Println(" Ready!")
					return nil
				}
			}
		}

		fmt.Print(".")
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("timeout waiting for K3s")
}

// Stop stops the K3s container
// Returns nil if container doesn't exist or is already stopped (idempotent)
// Returns error for Docker infrastructure failures
func (m *Manager) Stop(ctx context.Context) error {
	containerName := m.containerName()
	exists, running, err := m.docker.ContainerState(ctx, containerName)
	if err != nil {
		return fmt.Errorf("failed to check container state: %w", err)
	}
	// Skip if container doesn't exist or is not running
	if !exists || !running {
		return nil
	}
	return m.docker.ContainerStop(ctx, containerName, 10*time.Second)
}

// Remove removes the K3s container
func (m *Manager) Remove(ctx context.Context) error {
	return m.docker.ContainerRemove(ctx, m.containerName())
}

// BuildImage builds an image and loads it into K3s containerd
// dockerfile is the path to the Dockerfile relative to contextDir (empty string for default "Dockerfile")
func (m *Manager) BuildImage(ctx context.Context, projectName, serviceName, contextDir, dockerfile string, buildArgs map[string]*string) error {
	imageName := fmt.Sprintf("%s-%s:latest", projectName, serviceName)

	// Build with docker SDK
	fmt.Printf("Building image %s from %s\n", imageName, contextDir)

	// If no dockerfile specified, use default "Dockerfile"
	dockerfilePath := dockerfile
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}

	if err := m.docker.ImageBuild(ctx, contextDir, dockerfilePath, imageName, buildArgs); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	// Save and load into k3s containerd (uses pipe to avoid tarball on disk)
	fmt.Printf("Loading image into K3s...\n")

	// Get image as tar stream
	imageTar, err := m.docker.ImageSave(ctx, imageName)
	if err != nil {
		return fmt.Errorf("docker save failed: %w", err)
	}
	defer func() { _ = imageTar.Close() }()

	containerName := m.containerName()

	// Import into K3s containerd via docker exec
	if err := m.docker.ContainerExecStream(ctx, containerName,
		[]string{"ctr", "images", "import", "-"},
		imageTar, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("ctr import failed: %w", err)
	}

	return nil
}

// CleanRuntime removes the runtime directory, the Docker volume for K3s data,
// and the Docker bridge network.
func (m *Manager) CleanRuntime() error {
	ctx := context.Background()

	// Remove the Docker volume for K3s data
	volumeName := m.getK3sDataVolumeName()
	if err := m.docker.VolumeRemove(ctx, volumeName); err != nil {
		// Log but don't fail if volume removal fails
		fmt.Printf("Warning: failed to remove volume %s: %v\n", volumeName, err)
	}

	// Remove the Docker bridge network
	networkName := m.networkName()
	if err := m.docker.NetworkRemove(ctx, networkName); err != nil {
		fmt.Printf("Warning: failed to remove network %s: %v\n", networkName, err)
	}

	return os.RemoveAll(m.runtimeDir)
}

// LoadImageFromTar loads an image from a tar reader into K3s containerd
func (m *Manager) LoadImageFromTar(ctx context.Context, imageTar io.Reader) error {
	return m.docker.ContainerExecStream(ctx, m.containerName(),
		[]string{"ctr", "images", "import", "-"},
		imageTar, os.Stdout, os.Stderr)
}

// LoadInitImage builds a minimal init container image from the local kappal-init
// binary and loads it into K3s containerd. This ensures the init container image
// always matches the running kappal version, regardless of what's in the registry.
func (m *Manager) LoadInitImage(ctx context.Context, imageName string) error {
	// Find kappal-init binary in the current environment
	kappalInitPath := "/usr/local/bin/kappal-init"
	if _, err := os.Stat(kappalInitPath); err != nil {
		// Not available locally; K3s will try to pull from registry
		return nil
	}

	// Create temp build context with a minimal Dockerfile
	tmpDir, err := os.MkdirTemp("", "kappal-init-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Write Dockerfile — scratch is fine since kappal-init is statically linked (CGO_ENABLED=0)
	dockerfile := "FROM scratch\nCOPY kappal-init /usr/local/bin/kappal-init\nENTRYPOINT [\"/usr/local/bin/kappal-init\"]\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	// Copy the binary into the build context
	data, err := os.ReadFile(kappalInitPath)
	if err != nil {
		return fmt.Errorf("failed to read kappal-init binary: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "kappal-init"), data, 0755); err != nil {
		return fmt.Errorf("failed to write kappal-init to build context: %w", err)
	}

	// Build the minimal init image
	if err := m.docker.ImageBuild(ctx, tmpDir, "Dockerfile", imageName, nil); err != nil {
		return fmt.Errorf("failed to build init image: %w", err)
	}

	// Save and load into K3s containerd
	imageTar, err := m.docker.ImageSave(ctx, imageName)
	if err != nil {
		return fmt.Errorf("failed to save init image: %w", err)
	}
	defer func() { _ = imageTar.Close() }()

	return m.docker.ContainerExecStream(ctx, m.containerName(),
		[]string{"ctr", "images", "import", "-"},
		imageTar, io.Discard, os.Stderr)
}
