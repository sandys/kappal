package k3s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/kappal-app/kappal/pkg/docker"
	"github.com/kappal-app/kappal/pkg/k8s"
)

const (
	K3sImage      = "docker.io/rancher/k3s:v1.29.0-k3s1"
	ContainerName = "kappal-k3s"
)

// Manager handles K3s lifecycle only (Docker start/stop)
// All Kubernetes operations go through client-go after K3s is running
type Manager struct {
	workspaceDir string
	runtimeDir   string
	docker       *docker.Client
}

// NewManager creates a new K3s manager
func NewManager(workspaceDir string) (*Manager, error) {
	dockerClient, err := docker.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	return &Manager{
		workspaceDir: workspaceDir,
		runtimeDir:   filepath.Join(workspaceDir, "runtime"),
		docker:       dockerClient,
	}, nil
}

// Close closes the Docker client
func (m *Manager) Close() error {
	if m.docker != nil {
		return m.docker.Close()
	}
	return nil
}

// getVolumeNamePrefix returns a unique prefix for named Docker volumes based on workspace path.
// This ensures volumes are isolated per project directory.
func (m *Manager) getVolumeNamePrefix() string {
	// Use hash of absolute workspace path for uniqueness
	absPath, err := filepath.Abs(m.workspaceDir)
	if err != nil {
		absPath = m.workspaceDir
	}
	hash := sha256.Sum256([]byte(absPath))
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

// EnsureRunning starts K3s if not already running
func (m *Manager) EnsureRunning(ctx context.Context) error {
	// Check if container exists and its state
	exists, running, err := m.docker.ContainerState(ctx, ContainerName)
	if err != nil {
		return err
	}

	if running {
		fmt.Println("K3s already running")
		return m.waitForReady(ctx)
	}

	// Remove any existing stopped/dead container before starting fresh
	if exists {
		if err := m.docker.ContainerRemove(ctx, ContainerName); err != nil {
			return fmt.Errorf("failed to remove existing container: %w", err)
		}
	}

	// Create and start new container
	return m.start(ctx)
}

func (m *Manager) start(ctx context.Context) error {
	fmt.Println("Starting K3s...")

	// Create runtime directory if it doesn't exist
	if err := os.MkdirAll(m.runtimeDir, 0755); err != nil {
		return fmt.Errorf("failed to create runtime directory: %w", err)
	}

	// Use a named Docker volume for K3s data persistence.
	// This works correctly regardless of whether kappal is running in a container
	// or on the host, avoiding bind mount path translation issues.
	k3sDataVolume := m.getK3sDataVolumeName()

	// Build container config
	config := &container.Config{
		Image: K3sImage,
		Cmd: []string{
			"server",
			"--disable=traefik",
			"--disable=metrics-server",
		},
		Env: []string{
			"K3S_KUBECONFIG_MODE=644",
		},
	}

	// Build host config with privileged mode and host networking
	hostConfig := &container.HostConfig{
		Privileged:    true,
		NetworkMode:   "host",
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: k3sDataVolume,
				Target: "/var/lib/rancher/k3s",
			},
		},
	}

	if err := m.docker.ContainerRun(ctx, config, hostConfig, ContainerName); err != nil {
		return fmt.Errorf("failed to start K3s: %w", err)
	}

	return m.waitForReady(ctx)
}

// waitForReady waits for K3s to be ready and extracts the kubeconfig
func (m *Manager) waitForReady(ctx context.Context) error {
	fmt.Print("Waiting for K3s to be ready")

	// Ensure runtime directory exists
	if err := os.MkdirAll(m.runtimeDir, 0755); err != nil {
		return fmt.Errorf("failed to create runtime directory: %w", err)
	}

	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		// Extract kubeconfig using docker exec cat (more reliable than docker cp -)
		output, err := m.docker.ContainerExec(ctx, ContainerName, []string{"cat", "/etc/rancher/k3s/k3s.yaml"})
		if err == nil && len(output) > 0 {
			// Write kubeconfig to runtime directory
			kubeconfigPath := m.GetKubeconfigPath()
			if err := os.WriteFile(kubeconfigPath, output, 0644); err != nil {
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
	exists, running, err := m.docker.ContainerState(ctx, ContainerName)
	if err != nil {
		return fmt.Errorf("failed to check container state: %w", err)
	}
	// Skip if container doesn't exist or is not running
	if !exists || !running {
		return nil
	}
	return m.docker.ContainerStop(ctx, ContainerName, 10*time.Second)
}

// Remove removes the K3s container
func (m *Manager) Remove(ctx context.Context) error {
	return m.docker.ContainerRemove(ctx, ContainerName)
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

	// Import into K3s containerd via docker exec
	if err := m.docker.ContainerExecStream(ctx, ContainerName,
		[]string{"ctr", "images", "import", "-"},
		imageTar, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("ctr import failed: %w", err)
	}

	return nil
}

// CleanRuntime removes the runtime directory and the Docker volume for K3s data
func (m *Manager) CleanRuntime() error {
	ctx := context.Background()

	// Remove the Docker volume for K3s data
	volumeName := m.getK3sDataVolumeName()
	if err := m.docker.VolumeRemove(ctx, volumeName); err != nil {
		// Log but don't fail if volume removal fails
		fmt.Printf("Warning: failed to remove volume %s: %v\n", volumeName, err)
	}

	return os.RemoveAll(m.runtimeDir)
}

// LoadImageFromTar loads an image from a tar reader into K3s containerd
func (m *Manager) LoadImageFromTar(ctx context.Context, imageTar io.Reader) error {
	return m.docker.ContainerExecStream(ctx, ContainerName,
		[]string{"ctr", "images", "import", "-"},
		imageTar, os.Stdout, os.Stderr)
}
