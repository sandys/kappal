package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kappal-app/kappal/pkg/compose"
	"github.com/kappal-app/kappal/pkg/k3s"
	"github.com/kappal-app/kappal/pkg/tanka"
	"github.com/kappal-app/kappal/pkg/workspace"
	"github.com/spf13/cobra"
)

var (
	downVolumes bool
	downAll     bool
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop and remove containers",
	Long: `Stop and remove containers, networks, and K3s.

By default, this stops all services and K3s. Volume data is preserved.
Use --volumes/-v to also remove persistent volume data.`,
	RunE: runDown,
}

func init() {
	downCmd.Flags().BoolVarP(&downVolumes, "volumes", "v", false, "Remove named volumes and K3s data")
	downCmd.Flags().BoolVar(&downAll, "all", false, "Remove everything including K3s (deprecated, now default)")
}

func runDown(cmd *cobra.Command, args []string) error {
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
	_, err = workspace.Open(workspaceDir)
	if err != nil {
		return fmt.Errorf("workspace not found (run 'kappal up' first): %w", err)
	}

	k3sManager, err := k3s.NewManager(workspaceDir, project.Name)
	if err != nil {
		return fmt.Errorf("failed to create K3s manager: %w", err)
	}
	defer func() { _ = k3sManager.Close() }()

	kubeconfigPath := k3sManager.GetKubeconfigPath()

	// Delete resources via Tanka (uses kubeconfig, NOT docker exec kubectl)
	// If --volumes flag, delete everything including PVCs. Otherwise preserve volumes.
	// Continue cleanup even if tanka delete fails (e.g. stale kubeconfig, K3s unreachable)
	if err := tanka.Delete(ctx, project.Name, kubeconfigPath, tanka.DeleteOpts{
		AutoApprove:   true,
		DeleteVolumes: downVolumes,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to delete resources (continuing cleanup): %v\n", err)
	} else {
		fmt.Printf("Stopped services for %s\n", project.Name)
	}

	// Always stop and remove K3s on down (matches docker-compose behavior)
	if err := k3sManager.Stop(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to stop K3s: %v\n", err)
	}
	if err := k3sManager.Remove(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to remove K3s container: %v\n", err)
	}
	fmt.Println("Stopped K3s")

	// Remove volumes and runtime data if --volumes flag is set
	if downVolumes {
		if err := k3sManager.CleanRuntime(); err != nil {
			return fmt.Errorf("failed to clean runtime: %w", err)
		}
		fmt.Println("Removed volumes and runtime data")
	}

	return nil
}
