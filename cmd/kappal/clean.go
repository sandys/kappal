package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kappal-app/kappal/pkg/docker"
	"github.com/kappal-app/kappal/pkg/k3s"
	"github.com/kappal-app/kappal/pkg/state"
	"github.com/spf13/cobra"
)

var (
	cleanAll bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove all Kappal resources and workspace",
	Long: `Remove Kappal resources: containers, networks, volumes, and the .kappal directory.

By default, cleans resources for the current project (determined by compose file
or -p flag). Use --all to discover and remove ALL Kappal resources across every
project on this Docker host.

What gets cleaned (per-project mode):
  - Stops and removes the project's K3s container
  - Removes the project's Docker bridge network
  - Removes the project's K3s data volume
  - Deletes the .kappal/ workspace directory

What gets cleaned (--all mode):
  - Stops and removes ALL K3s containers with kappal.io/project label
  - Removes ALL Docker bridge networks with kappal.io/project label
  - Removes ALL Docker volumes with kappal- prefix
  - Deletes the .kappal/ workspace directory in the current directory

Use --all when you have stale containers from old projects blocking ports,
orphaned networks or volumes from failed cleanups, or when you want to
restore Docker to a pristine (no-kappal) state.

Flags:
  --all            Clean ALL kappal resources across every project
  -f <path>        Compose file path (default: docker-compose.yaml)
  -p <name>        Override project name

Examples:
  kappal clean                Clean current project only
  kappal clean --all          Remove ALL kappal resources system-wide
  kappal -p myapp clean       Clean a specific project by name`,
	RunE: runClean,
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanAll, "all", false, "Remove ALL kappal resources across every project")
	rootCmd.AddCommand(cleanCmd)
}

func runClean(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	if cleanAll {
		return runCleanAll(ctx)
	}
	return runCleanProject(ctx)
}

// runCleanAll discovers and removes ALL kappal resources across every project.
func runCleanAll(ctx context.Context) error {
	dockerClient, err := docker.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer func() { _ = dockerClient.Close() }()

	var errors []string

	// 1. Stop and remove ALL kappal containers
	containers, err := dockerClient.ContainerListByLabelKey(ctx, "kappal.io/project")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not list kappal containers: %v\n", err)
	} else {
		for _, ctr := range containers {
			fmt.Printf("Stopping container %s (%s)...\n", ctr.Name, ctr.Status)
			if err := dockerClient.ContainerStop(ctx, ctr.Name, 10*time.Second); err != nil {
				errors = append(errors, fmt.Sprintf("stop %s: %v", ctr.Name, err))
			}
			fmt.Printf("Removing container %s...\n", ctr.Name)
			if err := dockerClient.ContainerRemove(ctx, ctr.Name); err != nil {
				errors = append(errors, fmt.Sprintf("remove %s: %v", ctr.Name, err))
			}
		}
		if len(containers) == 0 {
			fmt.Println("No kappal containers found")
		}
	}

	// 2. Remove ALL kappal networks
	networks, err := dockerClient.NetworkListByLabelKey(ctx, "kappal.io/project")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not list kappal networks: %v\n", err)
	} else {
		for _, netName := range networks {
			fmt.Printf("Removing network %s...\n", netName)
			if err := dockerClient.NetworkRemove(ctx, netName); err != nil {
				errors = append(errors, fmt.Sprintf("remove network %s: %v", netName, err))
			}
		}
		if len(networks) == 0 {
			fmt.Println("No kappal networks found")
		}
	}

	// 3. Remove ALL kappal volumes (kappal- prefix)
	volumes, err := dockerClient.VolumeListByPrefix(ctx, "kappal-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not list kappal volumes: %v\n", err)
	} else {
		for _, volName := range volumes {
			fmt.Printf("Removing volume %s...\n", volName)
			if err := dockerClient.VolumeRemove(ctx, volName); err != nil {
				errors = append(errors, fmt.Sprintf("remove volume %s: %v", volName, err))
			}
		}
		if len(volumes) == 0 {
			fmt.Println("No kappal volumes found")
		}
	}

	// 4. Remove .kappal directory in current working directory
	removeWorkspaceDir()

	if len(errors) > 0 {
		fmt.Fprintf(os.Stderr, "\nClean completed with %d warning(s):\n", len(errors))
		for _, e := range errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
	} else {
		fmt.Println("Clean complete — all kappal resources removed")
	}

	return nil
}

// runCleanProject cleans resources for the current project only.
func runCleanProject(ctx context.Context) error {
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
		dockerClient, err := docker.NewClient()
		if err == nil {
			_ = dockerClient.NetworkRemove(ctx, discovered.K3s.Network)
			_ = dockerClient.Close()
		}
	}

	// Remove the entire .kappal directory
	removeWorkspaceDir()

	fmt.Println("Clean complete")
	return nil
}

// removeWorkspaceDir removes the .kappal directory in the current working directory.
func removeWorkspaceDir() {
	projectDir, err := os.Getwd()
	if err != nil {
		return
	}
	workspaceDir := filepath.Join(projectDir, ".kappal")
	if _, err := os.Stat(workspaceDir); err == nil {
		fmt.Println("Removing .kappal directory...")
		if err := os.RemoveAll(workspaceDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove .kappal directory: %v\n", err)
		}
	}
}
