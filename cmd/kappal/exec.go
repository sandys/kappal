package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kappal-app/kappal/pkg/compose"
	"github.com/kappal-app/kappal/pkg/k8s"
	"github.com/kappal-app/kappal/pkg/state"
	"github.com/spf13/cobra"
)

var (
	execInteractive bool
	execTTY         bool
	execIndex       int
)

var execCmd = &cobra.Command{
	Use:   "exec [OPTIONS] SERVICE COMMAND [ARGS...]",
	Short: "Execute a command in a running service container",
	Long: `Execute a command in a running service container.

This is similar to 'docker compose exec' - it runs a command inside
a container of a running service.

Examples:
  kappal exec web sh                      # Start a shell in web service
  kappal exec -it web bash                # Start interactive bash
  kappal exec web wget -O - http://api    # Run wget in web container
  kappal exec --index 1 web ps aux        # Run in second replica`,
	Args: cobra.MinimumNArgs(2),
	RunE: runExec,
}

func init() {
	execCmd.Flags().BoolVarP(&execInteractive, "interactive", "i", false, "Keep STDIN open")
	execCmd.Flags().BoolVarP(&execTTY, "tty", "t", false, "Allocate a pseudo-TTY")
	execCmd.Flags().IntVar(&execIndex, "index", 0, "Index of the container if service has multiple replicas")
	// Disable interspersed flags so flags after SERVICE are passed to the command
	// This allows: kappal exec app sh -c 'echo hello' (without needing --)
	execCmd.Flags().SetInterspersed(false)
}

func runExec(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	serviceName := args[0]
	command := args[1:]

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

	// Verify service exists in compose file
	found := false
	for _, svc := range project.Services {
		if svc.Name == serviceName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("service %q not found in compose file", serviceName)
	}

	workspaceDir := filepath.Join(projectDir, ".kappal")

	// Discover live state via labels (fast path — no K8s query needed)
	discovered, err := state.Discover(ctx, project.Name, workspaceDir, state.DiscoverOpts{QueryK8s: false})
	if err != nil {
		return fmt.Errorf("failed to discover state: %w", err)
	}

	if discovered.K3s.Status != "running" {
		return fmt.Errorf("K3s not running (run 'kappal up' first)")
	}

	if discovered.Kubeconfig == "" {
		return fmt.Errorf("kubeconfig not available (run 'kappal up' first)")
	}

	// Create k8s client
	k8sClient, err := k8s.NewClient(discovered.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create k8s client: %w", err)
	}

	// If -it flags are both set, enable interactive TTY
	if execInteractive && execTTY {
		execInteractive = true
	}

	opts := k8s.ExecOptions{
		Stdin:       nil,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		TTY:         execTTY,
		Interactive: execInteractive,
		Index:       execIndex,
	}

	// Set stdin if interactive
	if execInteractive {
		opts.Stdin = os.Stdin
	}

	// Execute command in the service's pod
	return k8sClient.Exec(ctx, project.Name, serviceName, command, opts)
}
