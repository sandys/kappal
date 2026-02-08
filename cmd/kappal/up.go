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
	"github.com/kappal-app/kappal/pkg/state"
	"github.com/kappal-app/kappal/pkg/tanka"
	"github.com/kappal-app/kappal/pkg/transform"
	"github.com/kappal-app/kappal/pkg/workspace"
	"github.com/spf13/cobra"
)

var (
	upDetach  bool
	upBuild   bool
	upTimeout int
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Create and start containers",
	Long: `Create and start containers defined in the Compose file.

Parses docker-compose.yaml, generates Kubernetes manifests, ensures a K3s instance
is running for this project, and applies the manifests via Tanka. Waits up to 5
minutes for all pods to become ready before returning.

Services with "restart: no" run as one-shot Kubernetes Jobs. Services with
depends_on condition: service_completed_successfully get init containers that block
until the dependency Job finishes. Services with profiles are excluded.

Port chain: compose ports → K3s container port bindings → K8s NodePort services.
Published ports bind to the Docker host and are accessible via localhost.

Flags:
  -d, --detach       Run in the background (timeout becomes a warning, not an error)
  --build            Build images (from build.context in compose) before starting
  --timeout <secs>   Seconds to wait for services to be ready (default 300)
  -f <path>          Compose file path (default: docker-compose.yaml)
  -p <name>          Override project name

Examples:
  kappal up -d                  Start all services
  kappal up --build -d          Build images then start
  kappal up --timeout 600 -d    Wait up to 10 minutes for readiness
  kappal -p myapp up -d         Start with explicit project name`,
	RunE:  runUp,
}

func init() {
	upCmd.Flags().BoolVarP(&upDetach, "detach", "d", false, "Run containers in the background")
	upCmd.Flags().BoolVar(&upBuild, "build", false, "Build images before starting containers")
	upCmd.Flags().IntVar(&upTimeout, "timeout", 300, "Timeout in seconds waiting for services to be ready")
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
	resolvedName := resolveProjectName(projectName, filepath.Dir(composePath))
	project, err := compose.Load(composePath, resolvedName)
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

	// Discover existing state (if any) for awareness
	discovered, _ := state.Discover(ctx, project.Name, workspaceDir, state.DiscoverOpts{QueryK8s: false})
	if discovered != nil && discovered.K3s.Status == "running" {
		fmt.Println("K3s already running (discovered via labels)")
	}

	// Ensure K3s is running (ONLY Docker command - starts the container)
	k3sManager, err := k3s.NewManager(workspaceDir, project.Name)
	if err != nil {
		return fmt.Errorf("failed to create K3s manager: %w", err)
	}
	defer func() { _ = k3sManager.Close() }()

	// Extract published ports from compose project for K3s port forwarding
	var ports []k3s.PublishedPort
	for _, svc := range project.Services {
		if len(svc.Profiles) > 0 {
			continue
		}
		for _, p := range svc.Ports {
			published := p.Target
			if p.Published != "" {
				fmt.Sscanf(p.Published, "%d", &published)
			}
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			ports = append(ports, k3s.PublishedPort{
				HostPort:      uint32(published),
				ContainerPort: uint32(p.Target),
				Protocol:      proto,
			})
		}
	}
	if err := k3sManager.SetPublishedPorts(ports); err != nil {
		return err
	}

	if err := k3sManager.EnsureRunning(ctx); err != nil {
		return fmt.Errorf("failed to start K3s: %w", err)
	}

	kubeconfigPath := k3sManager.GetKubeconfigPath()

	// Build images if requested
	if upBuild {
		for _, svc := range project.Services {
			if len(svc.Profiles) > 0 {
				continue
			}
			if svc.Build != nil {
				fmt.Printf("Building %s...\n", svc.Name)
				dockerfile := ""
				if svc.Build.Dockerfile != "" {
					dockerfile = svc.Build.Dockerfile
				}

				// Only pass explicit build.args from compose file
				if err := k3sManager.BuildImage(ctx, project.Name, svc.Name, svc.Build.Context, dockerfile, svc.Build.Args); err != nil {
					return fmt.Errorf("failed to build %s: %w", svc.Name, err)
				}
			}
		}
	}

	// Load kappal init image into K3s if any service has Job dependencies
	// (needed for init containers that wait for job completion)
	hasJobDeps := false
	for _, svc := range project.Services {
		if len(svc.Profiles) > 0 {
			continue
		}
		for depName, depConfig := range svc.DependsOn {
			if depConfig.Condition == "service_completed_successfully" {
				// Check if the dependency is a Job (restart: "no")
				if depSvc, ok := project.Services[depName]; ok && depSvc.Restart == "no" {
					hasJobDeps = true
					break
				}
			}
		}
		if hasJobDeps {
			break
		}
	}
	if hasJobDeps {
		if err := k3sManager.LoadInitImage(ctx, transform.KappalInitImage); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not pre-load init image: %v\n", err)
		}
	}

	// Delete existing Jobs before re-applying (Jobs are immutable in K8s)
	deleteCtx, deleteCancel := context.WithTimeout(ctx, 10*time.Second)
	defer deleteCancel()
	if k8sClient, err := k8s.NewClient(kubeconfigPath); err == nil {
		_ = k8sClient.DeleteJobs(deleteCtx, project.Name)
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
	if err := k8sClient.WaitForPodsReady(ctx, project.Name, labelSelector, time.Duration(upTimeout)*time.Second); err != nil {
		if upDetach {
			fmt.Fprintf(os.Stderr, "Warning: %v (services may still be starting)\n", err)
			fmt.Println("Services starting in background. Use 'kappal ps' to check status.")
		} else {
			return fmt.Errorf("services not ready: %w", err)
		}
	} else {
		fmt.Println("Services started successfully!")
	}

	return nil
}
