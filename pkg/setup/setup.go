package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kappal-app/kappal/pkg/docker"
	"github.com/kappal-app/kappal/pkg/k3s"
)

// Metadata stores setup state
type Metadata struct {
	Version  string    `json:"version"`
	K3sImage string    `json:"k3s_image"`
	SetupAt  time.Time `json:"setup_at"`
}

// WorkspaceDir returns the kappal workspace directory (.kappal in current dir)
func WorkspaceDir() string {
	return ".kappal"
}

// MetadataPath returns path to setup.json
func MetadataPath() string {
	return filepath.Join(WorkspaceDir(), "setup.json")
}

// Run executes the setup process
func Run(ctx context.Context) error {
	// 1. Verify Docker daemon
	fmt.Print("Checking Docker daemon... ")
	dockerClient, err := docker.NewClient()
	if err != nil {
		fmt.Println("FAILED")
		return fmt.Errorf("docker is not running: %w", err)
	}
	defer func() { _ = dockerClient.Close() }()
	fmt.Println("OK")

	// 2. Pull K3s image
	fmt.Printf("Pulling K3s image (%s)... ", k3s.K3sImage)
	if err := dockerClient.ImagePull(ctx, k3s.K3sImage); err != nil {
		fmt.Println("FAILED")
		return fmt.Errorf("failed to pull K3s image: %w", err)
	}
	fmt.Println("OK")

	// 3. Create .kappal directory
	if err := os.MkdirAll(WorkspaceDir(), 0755); err != nil {
		return fmt.Errorf("failed to create .kappal directory: %w", err)
	}

	// 4. Write metadata
	metadata := Metadata{
		Version:  "1.0.0",
		K3sImage: k3s.K3sImage,
		SetupAt:  time.Now(),
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if err := os.WriteFile(MetadataPath(), data, 0644); err != nil {
		return fmt.Errorf("failed to write setup metadata: %w", err)
	}

	fmt.Println("\nSetup complete! You can now use kappal commands.")
	return nil
}
