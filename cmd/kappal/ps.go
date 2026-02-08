package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/kappal-app/kappal/pkg/compose"
	"github.com/kappal-app/kappal/pkg/state"
	"github.com/spf13/cobra"
)

var (
	psFormat string
	psAll    bool
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List containers",
	Long: `List containers and their status.

Queries the Kubernetes API via client-go to show the current state of all services
in the project. By default outputs an aligned table; use -o json for machine-readable
output or -o yaml for YAML.

Table columns:
  NAME     Service name from docker-compose.yaml
  STATUS   Pod phase (Running, Pending, Succeeded, Failed)
  PORTS    Published host:container port mappings

For richer machine-readable output with replicas, pod IPs, and K3s state, use
"kappal inspect" instead.

Flags:
  -o, --format <fmt>   Output format: table (default), json, yaml
  -a, --all            Show all containers including stopped
  -f <path>            Compose file path (default: docker-compose.yaml)
  -p <name>            Override project name

Examples:
  kappal ps                  Table view of all services
  kappal ps -o json          JSON output for scripting
  kappal ps -o json | jq '.[] | select(.Status=="Running")'
                             Filter running services`,
	RunE: runPs,
}

func init() {
	psCmd.Flags().StringVarP(&psFormat, "format", "o", "table", "Output format (table, json, yaml)")
	psCmd.Flags().BoolVarP(&psAll, "all", "a", false, "Show all containers (including stopped)")
}

// psEntry is the simplified service status for ps output.
type psEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Ports  string `json:"ports"`
}

func runPs(cmd *cobra.Command, args []string) error {
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

	// Discover live state via labels
	discovered, err := state.Discover(ctx, project.Name, workspaceDir, state.DiscoverOpts{QueryK8s: true})
	if err != nil {
		return fmt.Errorf("failed to discover state: %w", err)
	}

	// Merge compose definitions with discovered K8s state
	merged := state.MergeCompose(discovered, project)

	// Convert to ps entries
	var entries []psEntry
	for _, svc := range merged {
		var portStrs []string
		for _, p := range svc.Ports {
			portStrs = append(portStrs, fmt.Sprintf("%d->%d/%s", p.Host, p.Container, p.Protocol))
		}
		entries = append(entries, psEntry{
			Name:   svc.Name,
			Status: svc.Status,
			Ports:  strings.Join(portStrs, ", "),
		})
	}

	switch psFormat {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	case "yaml":
		for _, s := range entries {
			fmt.Printf("- name: %s\n  status: %s\n  ports: %s\n", s.Name, s.Status, s.Ports)
		}
		return nil
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tPORTS")
		for _, s := range entries {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, s.Status, s.Ports)
		}
		return w.Flush()
	}
}
