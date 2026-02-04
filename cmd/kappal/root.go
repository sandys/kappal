package main

import (
	"github.com/spf13/cobra"
)

var (
	composeFile string
	projectName string
)

var rootCmd = &cobra.Command{
	Use:   "kappal",
	Short: "Docker Compose CLI powered by Kubernetes",
	Long: `Kappal is a drop-in replacement for Docker Compose that uses
K3s and Kubernetes under the hood. Users never see Kubernetes -
just familiar Compose commands.`,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&composeFile, "file", "f", "docker-compose.yaml", "Compose file path")
	rootCmd.PersistentFlags().StringVarP(&projectName, "project-name", "p", "", "Project name (defaults to directory name)")

	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(ejectCmd)
}
