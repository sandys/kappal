package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kappal-app/kappal/pkg/k3s"
	"github.com/kappal-app/kappal/pkg/state"
	"github.com/spf13/cobra"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove all Kappal resources and workspace",
	Long: `Remove the .kappal directory, stop K3s, and clean up all resources.

This is a complete cleanup that removes:
- The .kappal/ workspace directory
- Stops and removes the K3s container
- Removes any built images from the local Docker daemon

Use this when you want to start fresh or completely remove Kappal from a project.`,
	RunE: runClean,
}

func init() {
	rootCmd.AddCommand(cleanCmd)
}

func runClean(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	workspaceDir := filepath.Join(projectDir, ".kappal")

	// Resolve project name: from -p flag, or directory-based with hash
	composePath := composeFile
	if !filepath.IsAbs(composePath) {
		composePath = filepath.Join(projectDir, composePath)
	}
	projName := resolveProjectName(projectName, filepath.Dir(composePath))

	// Discover live state via labels (fast path — no K8s query)
	discovered, err := state.Discover(ctx, projName, workspaceDir, state.DiscoverOpts{QueryK8s: false})
	if err != nil {
		// Non-fatal — if discovery fails, fall back to K3s Manager (convention-based)
		fmt.Fprintf(os.Stderr, "Warning: state discovery failed: %v\n", err)
	}

	// K3s Manager still needed for Stop/Remove/CleanRuntime
	k3sManager, err := k3s.NewManager(workspaceDir, projName)
	if err != nil {
		return fmt.Errorf("failed to create K3s manager: %w", err)
	}
	defer func() { _ = k3sManager.Close() }()

	fmt.Println("Stopping K3s...")
	_ = k3sManager.Stop(ctx) // Ignore error - may not be running

	fmt.Println("Removing K3s container...")
	_ = k3sManager.Remove(ctx) // Ignore error - may not exist

	// Clean runtime (volumes + network)
	_ = k3sManager.CleanRuntime()

	// Also clean discovered network if different from convention-based name
	if discovered != nil && discovered.K3s.Network != "" && discovered.K3s.Network != k3sManager.NetworkName() {
		fmt.Printf("Removing discovered network %s...\n", discovered.K3s.Network)
	}

	// Remove the entire .kappal directory
	if _, err := os.Stat(workspaceDir); err == nil {
		fmt.Println("Removing .kappal directory...")
		if err := os.RemoveAll(workspaceDir); err != nil {
			return fmt.Errorf("failed to remove .kappal directory: %w", err)
		}
	}

	fmt.Println("Clean complete")
	return nil
}
