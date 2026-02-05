package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kappal-app/kappal/pkg/compose"
	"github.com/kappal-app/kappal/pkg/k3s"
	"github.com/spf13/cobra"
)

var buildCmd = &cobra.Command{
	Use:   "build [SERVICE...]",
	Short: "Build or rebuild services",
	Long:  `Build images for services with a build context defined.`,
	RunE:  runBuild,
}

func runBuild(cmd *cobra.Command, args []string) error {
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
	k3sManager, err := k3s.NewManager(workspaceDir)
	if err != nil {
		return fmt.Errorf("failed to create K3s manager: %w", err)
	}
	defer func() { _ = k3sManager.Close() }()

	// Ensure K3s is running (for loading images into containerd)
	if err := k3sManager.EnsureRunning(ctx); err != nil {
		return fmt.Errorf("failed to start K3s: %w", err)
	}

	// Build services
	servicesToBuild := args
	if len(servicesToBuild) == 0 {
		for _, svc := range project.Services {
			if svc.Build != nil {
				servicesToBuild = append(servicesToBuild, svc.Name)
			}
		}
	}

	if len(servicesToBuild) == 0 {
		fmt.Println("No services with build context found")
		return nil
	}

	for _, name := range servicesToBuild {
		svc, err := project.GetService(name)
		if err != nil {
			return fmt.Errorf("service %s not found: %w", name, err)
		}
		if svc.Build == nil {
			fmt.Printf("Skipping %s (no build context)\n", name)
			continue
		}

		fmt.Printf("Building %s...\n", name)
		dockerfile := ""
		if svc.Build.Dockerfile != "" {
			dockerfile = svc.Build.Dockerfile
		}

		// Only pass explicit build.args from compose file
		if err := k3sManager.BuildImage(ctx, project.Name, name, svc.Build.Context, dockerfile, svc.Build.Args); err != nil {
			return fmt.Errorf("failed to build %s: %w", name, err)
		}
		fmt.Printf("Built %s\n", name)
	}

	return nil
}
