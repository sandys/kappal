package k3s

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
}

// NewManager creates a new K3s manager
func NewManager(workspaceDir string) *Manager {
	return &Manager{
		workspaceDir: workspaceDir,
		runtimeDir:   filepath.Join(workspaceDir, "runtime"),
	}
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
	running, exists := m.containerState(ctx)

	if running {
		fmt.Println("K3s already running")
		return m.waitForReady(ctx)
	}

	// Remove any existing stopped/dead container before starting fresh
	if exists {
		if err := exec.CommandContext(ctx, "docker", "rm", "-f", ContainerName).Run(); err != nil {
			return fmt.Errorf("failed to remove existing container: %w", err)
		}
	}

	// Create and start new container
	return m.start(ctx)
}

// containerState returns (running, exists) for the K3s container
func (m *Manager) containerState(ctx context.Context) (running bool, exists bool) {
	// Check if container exists at all
	inspectCmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Status}}", ContainerName)
	output, err := inspectCmd.Output()
	if err != nil {
		return false, false // Container doesn't exist
	}

	status := strings.TrimSpace(string(output))
	return status == "running", true
}

func (m *Manager) start(ctx context.Context) error {
	fmt.Println("Starting K3s...")

	// Create runtime directory (clean start to avoid stale node records)
	os.RemoveAll(m.runtimeDir)
	if err := os.MkdirAll(m.runtimeDir, 0755); err != nil {
		return fmt.Errorf("failed to create runtime directory: %w", err)
	}

	// Start K3s in Docker
	args := []string{
		"run", "-d",
		"--name", ContainerName,
		"--privileged",
		"--restart", "unless-stopped",
		"-p", "6443:6443",
		"-p", "80:80",
		"-p", "443:443",
		"-e", "K3S_KUBECONFIG_MODE=644",
		K3sImage,
		"server",
		"--disable=traefik",
		"--disable=metrics-server",
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
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
		cmd := exec.CommandContext(ctx, "docker", "exec", ContainerName, "cat", "/etc/rancher/k3s/k3s.yaml")
		output, err := cmd.Output()
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
func (m *Manager) Stop(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "stop", ContainerName)
	return cmd.Run()
}

// Remove removes the K3s container
func (m *Manager) Remove(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", ContainerName)
	return cmd.Run()
}

// BuildImage builds an image and loads it into K3s containerd
func (m *Manager) BuildImage(ctx context.Context, projectName, serviceName, contextDir string) error {
	imageName := fmt.Sprintf("%s-%s:latest", projectName, serviceName)

	// Build with docker
	fmt.Printf("Building image %s from %s\n", imageName, contextDir)

	buildCmd := exec.CommandContext(ctx, "docker", "build", "-t", imageName, contextDir)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	// Save and load into k3s containerd (uses pipe to avoid tarball on disk)
	fmt.Printf("Loading image into K3s...\n")

	saveCmd := exec.CommandContext(ctx, "docker", "save", imageName)
	loadCmd := exec.CommandContext(ctx, "docker", "exec", "-i", ContainerName, "ctr", "images", "import", "-")

	pipe, err := saveCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	loadCmd.Stdin = pipe
	loadCmd.Stdout = os.Stdout
	loadCmd.Stderr = os.Stderr

	if err := saveCmd.Start(); err != nil {
		return fmt.Errorf("docker save failed to start: %w", err)
	}
	if err := loadCmd.Start(); err != nil {
		return fmt.Errorf("ctr import failed to start: %w", err)
	}

	if err := saveCmd.Wait(); err != nil {
		return fmt.Errorf("docker save failed: %w", err)
	}
	if err := loadCmd.Wait(); err != nil {
		return fmt.Errorf("ctr import failed: %w", err)
	}

	return nil
}

// CleanRuntime removes the runtime directory
func (m *Manager) CleanRuntime() error {
	return os.RemoveAll(m.runtimeDir)
}
