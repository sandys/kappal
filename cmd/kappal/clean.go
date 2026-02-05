package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kappal-app/kappal/pkg/k3s"
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

	// Stop and remove K3s container if it exists
	k3sManager, err := k3s.NewManager(workspaceDir)
	if err != nil {
		return fmt.Errorf("failed to create K3s manager: %w", err)
	}
	defer func() { _ = k3sManager.Close() }()

	fmt.Println("Stopping K3s...")
	_ = k3sManager.Stop(ctx) // Ignore error - may not be running

	fmt.Println("Removing K3s container...")
	_ = k3sManager.Remove(ctx) // Ignore error - may not exist

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
