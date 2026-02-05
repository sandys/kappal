package main

import (
	"context"
	"os"

	"github.com/kappal-app/kappal/pkg/setup"
	"github.com/spf13/cobra"
)

var (
	composeFile string
	projectName string
	runSetup    bool
)

var rootCmd = &cobra.Command{
	Use:   "kappal",
	Short: "Docker Compose CLI powered by Kubernetes",
	Long: `Kappal is a drop-in replacement for Docker Compose that uses
K3s and Kubernetes under the hood. Users never see Kubernetes -
just familiar Compose commands.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Handle --setup flag - run setup and exit
		if runSetup {
			return nil // Setup handled in Run
		}

		// Skip prerequisite check for certain commands
		skipCheck := map[string]bool{
			"kappal":  true, // root command shows help
			"help":    true,
			"version": true,
			"clean":   true, // clean should work even without setup
		}
		if skipCheck[cmd.Name()] {
			return nil
		}

		// Also skip if help flag is set (e.g., kappal --help, kappal up --help)
		if helpFlag, _ := cmd.Flags().GetBool("help"); helpFlag {
			return nil
		}

		// Check prerequisites for all other commands
		return setup.Check()
	},
	Run: func(cmd *cobra.Command, args []string) {
		// If --setup was passed, run setup
		if runSetup {
			if err := setup.Run(context.Background()); err != nil {
				cmd.PrintErrln(err)
				os.Exit(1)
			}
			return
		}
		// Otherwise show help
		_ = cmd.Help()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&composeFile, "file", "f", "docker-compose.yaml", "Compose file path")
	rootCmd.PersistentFlags().StringVarP(&projectName, "project-name", "p", "", "Project name (defaults to directory name)")

	// Add --setup flag
	rootCmd.Flags().BoolVar(&runSetup, "setup", false, "Set up kappal (pull K3s image, verify Docker)")

	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(ejectCmd)
}
