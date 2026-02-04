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
	Long:  `Stop and remove containers, networks, and optionally volumes.`,
	RunE:  runDown,
}

func init() {
	downCmd.Flags().BoolVarP(&downVolumes, "volumes", "v", false, "Remove named volumes and K3s data")
	downCmd.Flags().BoolVar(&downAll, "all", false, "Remove everything including K3s")
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

	project, err := compose.Load(composePath, projectName)
	if err != nil {
		return fmt.Errorf("failed to load compose file: %w", err)
	}

	workspaceDir := filepath.Join(projectDir, ".kappal")
	_, err = workspace.Open(workspaceDir)
	if err != nil {
		return fmt.Errorf("workspace not found (run 'kappal up' first): %w", err)
	}

	k3sManager := k3s.NewManager(workspaceDir)
	kubeconfigPath := k3sManager.GetKubeconfigPath()

	// Delete resources via Tanka (uses kubeconfig, NOT docker exec kubectl)
	if err := tanka.Delete(ctx, project.Name, kubeconfigPath, tanka.DeleteOpts{AutoApprove: true}); err != nil {
		return fmt.Errorf("failed to delete resources: %w", err)
	}

	fmt.Printf("Stopped services for %s\n", project.Name)

	// Stop K3s if --all or --volumes
	if downAll || downVolumes {
		if err := k3sManager.Stop(ctx); err != nil {
			return fmt.Errorf("failed to stop K3s: %w", err)
		}
		fmt.Println("Stopped K3s")

		if downVolumes {
			if err := k3sManager.CleanRuntime(); err != nil {
				return fmt.Errorf("failed to clean runtime: %w", err)
			}
			fmt.Println("Removed volumes and runtime data")
		}
	}

	return nil
}
