package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kappal-app/kappal/pkg/compose"
	"github.com/kappal-app/kappal/pkg/transform"
	"github.com/kappal-app/kappal/pkg/workspace"
	"github.com/spf13/cobra"
)

var (
	ejectOutput string
)

var ejectCmd = &cobra.Command{
	Use:   "eject",
	Short: "Export as standalone Tanka workspace",
	Long: `Export the generated Jsonnet as a standalone Tanka workspace.

Creates a self-contained Tanka project directory with Jsonnet files, a jsonnetfile
for dependency management, and an environments/default configuration. This lets you
customize Kubernetes manifests directly and manage them with standard Tanka tooling
(tk show, tk apply, tk diff).

The ejected workspace is independent of kappal — you can modify the Jsonnet, add
resources, or integrate it into a GitOps pipeline. After ejecting, use "jb install"
to fetch Jsonnet dependencies, then "tk show environments/default" to preview
manifests.

Flags:
  -o, --output <dir>   Output directory (default: "tanka")
  -f <path>            Compose file path (default: docker-compose.yaml)
  -p <name>            Override project name

Examples:
  kappal eject                    Export to ./tanka/
  kappal eject -o k8s-manifests   Export to ./k8s-manifests/`,
	RunE: runEject,
}

func init() {
	ejectCmd.Flags().StringVarP(&ejectOutput, "output", "o", "tanka", "Output directory for Tanka workspace")
}

func runEject(cmd *cobra.Command, args []string) error {
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

	outputDir := ejectOutput
	if !filepath.IsAbs(outputDir) {
		outputDir = filepath.Join(projectDir, outputDir)
	}

	// Create standalone workspace
	ws, err := workspace.New(outputDir)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	transformer := transform.NewTransformer(project)
	if err := transformer.GenerateStandalone(ws); err != nil {
		return fmt.Errorf("failed to generate workspace: %w", err)
	}

	fmt.Printf("Ejected to %s/\n", ejectOutput)
	fmt.Println("\nTo use with Tanka:")
	fmt.Printf("  cd %s\n", ejectOutput)
	fmt.Println("  jb install")
	fmt.Println("  tk show environments/default")
	fmt.Println("  tk apply environments/default")

	return nil
}
