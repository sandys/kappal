package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/kappal-app/kappal/pkg/compose"
	"github.com/kappal-app/kappal/pkg/k3s"
	"github.com/kappal-app/kappal/pkg/k8s"
	"github.com/spf13/cobra"
)

var (
	psFormat string
	psAll    bool
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List containers",
	Long:  `List containers and their status.`,
	RunE:  runPs,
}

func init() {
	psCmd.Flags().StringVarP(&psFormat, "format", "o", "table", "Output format (table, json, yaml)")
	psCmd.Flags().BoolVarP(&psAll, "all", "a", false, "Show all containers (including stopped)")
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

	project, err := compose.Load(composePath, projectName)
	if err != nil {
		return fmt.Errorf("failed to load compose file: %w", err)
	}

	workspaceDir := filepath.Join(projectDir, ".kappal")
	k3sManager := k3s.NewManager(workspaceDir)
	kubeconfigPath := k3sManager.GetKubeconfigPath()

	// Get status via client-go (NOT docker exec kubectl)
	k8sClient, err := k8s.NewClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to create k8s client: %w", err)
	}

	statuses, err := k8sClient.GetServiceStatuses(ctx, project)
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	switch psFormat {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(statuses)
	case "yaml":
		for _, s := range statuses {
			fmt.Printf("- name: %s\n  status: %s\n  ports: %s\n", s.Name, s.Status, s.Ports)
		}
		return nil
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tPORTS")
		for _, s := range statuses {
			fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, s.Status, s.Ports)
		}
		return w.Flush()
	}
}
