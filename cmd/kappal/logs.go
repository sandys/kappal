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
	logsFollow bool
	logsTail   int
)

var logsCmd = &cobra.Command{
	Use:   "logs [SERVICE...]",
	Short: "View output from containers",
	Long: `View output from containers. If no service is specified, shows logs from all services.

Streams logs from Kubernetes pods via client-go. Each line is prefixed with the
service name. When multiple services are shown, their logs are interleaved in real
time.

Without --follow, prints the last N lines (default 100) and exits (snapshot mode).
With --follow, streams new log lines continuously until interrupted (Ctrl+C).

Flags:
  --follow         Stream logs continuously (like tail -f)
  --tail <n>       Number of historical lines to show (default: 100)
  -f <path>        Compose file path (default: docker-compose.yaml)
  -p <name>        Override project name

Examples:
  kappal logs                All services, last 100 lines
  kappal logs api            Logs from the api service only
  kappal logs --follow api   Stream api logs continuously
  kappal logs --tail 20      Last 20 lines from all services`,
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().BoolVar(&logsFollow, "follow", false, "Follow log output")
	logsCmd.Flags().IntVar(&logsTail, "tail", 100, "Number of lines to show from the end")
}

func runLogs(cmd *cobra.Command, args []string) error {
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

	// Get logs via client-go
	k8sClient, err := k8s.NewClient(discovered.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create k8s client: %w", err)
	}

	opts := k8s.LogOptions{
		Follow:    logsFollow,
		TailLines: int64(logsTail),
		Services:  args,
	}

	return k8sClient.StreamLogs(ctx, project, opts, os.Stdout)
}
