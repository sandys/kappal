package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kappal-app/kappal/pkg/compose"
	"github.com/kappal-app/kappal/pkg/k3s"
	"github.com/kappal-app/kappal/pkg/k8s"
	"github.com/kappal-app/kappal/pkg/tanka"
	"github.com/kappal-app/kappal/pkg/transform"
	"github.com/kappal-app/kappal/pkg/workspace"
	"github.com/spf13/cobra"
)

var (
	upDetach bool
	upBuild  bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Create and start containers",
	Long:  `Create and start containers defined in the Compose file.`,
	RunE:  runUp,
}

func init() {
	upCmd.Flags().BoolVarP(&upDetach, "detach", "d", false, "Run containers in the background")
	upCmd.Flags().BoolVar(&upBuild, "build", false, "Build images before starting containers")
}

func runUp(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get project directory
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Resolve compose file path
	composePath := composeFile
	if !filepath.IsAbs(composePath) {
		composePath = filepath.Join(projectDir, composePath)
	}

	// Load compose file
	project, err := compose.Load(composePath, projectName)
	if err != nil {
		return fmt.Errorf("failed to load compose file: %w", err)
	}

	fmt.Printf("Project: %s\n", project.Name)

	// Create workspace directory
	workspaceDir := filepath.Join(projectDir, ".kappal")
	ws, err := workspace.New(workspaceDir)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	// Transform compose to Kubernetes manifests
	transformer := transform.NewTransformer(project)
	if err := transformer.Generate(ws); err != nil {
		return fmt.Errorf("failed to generate workspace: %w", err)
	}

	fmt.Println("Generated Kappal workspace in .kappal/")

	// Ensure K3s is running (ONLY Docker command - starts the container)
	k3sManager := k3s.NewManager(workspaceDir)
	if err := k3sManager.EnsureRunning(ctx); err != nil {
		return fmt.Errorf("failed to start K3s: %w", err)
	}

	kubeconfigPath := k3sManager.GetKubeconfigPath()

	// Build images if requested
	if upBuild {
		for _, svc := range project.Services {
			if svc.Build != nil {
				fmt.Printf("Building %s...\n", svc.Name)
				if err := k3sManager.BuildImage(ctx, project.Name, svc.Name, svc.Build.Context); err != nil {
					return fmt.Errorf("failed to build %s: %w", svc.Name, err)
				}
			}
		}
	}

	// Apply manifests via Tanka/kubectl (uses kubeconfig, NOT docker exec)
	if err := tanka.Apply(ctx, ws, kubeconfigPath, tanka.ApplyOpts{AutoApprove: true}); err != nil {
		return fmt.Errorf("failed to apply: %w", err)
	}

	// Wait for pods via client-go (NOT docker exec kubectl)
	k8sClient, err := k8s.NewClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to create k8s client: %w", err)
	}

	fmt.Println("Waiting for services to be ready...")
	labelSelector := fmt.Sprintf("kappal.io/project=%s", project.Name)
	if err := k8sClient.WaitForPodsReady(ctx, project.Name, labelSelector, 300*time.Second); err != nil {
		return fmt.Errorf("services not ready: %w", err)
	}

	fmt.Println("Services started successfully!")
	return nil
}
