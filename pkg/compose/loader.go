package compose

import (
	"context"
	"os"
	"path/filepath"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
)

// Load parses a docker-compose.yaml file and returns a Project
func Load(path string, projectName string) (*types.Project, error) {
	// Convert compose file path to absolute for reliable loading
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	// Get the directory containing the compose file
	absDir := filepath.Dir(absPath)

	// Check if .env file exists in the compose file's directory
	envFile := filepath.Join(absDir, ".env")
	var envFiles []string
	if _, err := os.Stat(envFile); err == nil {
		envFiles = append(envFiles, envFile)
	}

	// Build options - working directory and env files must be set before loading
	// Order matters: set env files first, then WithDotEnv loads them
	opts := []cli.ProjectOptionsFn{
		cli.WithWorkingDirectory(absDir),
		cli.WithOsEnv,
	}

	// Add env files if .env exists, then WithDotEnv to load them
	if len(envFiles) > 0 {
		opts = append(opts, cli.WithEnvFiles(envFiles...))
	}
	opts = append(opts, cli.WithDotEnv)

	if projectName != "" {
		opts = append(opts, cli.WithName(projectName))
	} else {
		// Use directory name as project name
		opts = append(opts, cli.WithName(filepath.Base(absDir)))
	}

	// Pass absolute path to ensure compose-go finds the file correctly
	options, err := cli.NewProjectOptions([]string{absPath}, opts...)
	if err != nil {
		return nil, err
	}

	return options.LoadProject(context.Background())
}

// LoadFromContent parses compose content from a byte slice
func LoadFromContent(content []byte, projectName string) (*types.Project, error) {
	// Write to temp file and load
	tmpDir, err := os.MkdirTemp("", "kappal-compose-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "docker-compose.yaml")
	if err := os.WriteFile(tmpFile, content, 0644); err != nil {
		return nil, err
	}

	return Load(tmpFile, projectName)
}

// GetServiceNames returns all service names in the project
func GetServiceNames(project *types.Project) []string {
	names := make([]string, 0, len(project.Services))
	for _, svc := range project.Services {
		names = append(names, svc.Name)
	}
	return names
}

// HasBuildContext returns true if any service has a build context
func HasBuildContext(project *types.Project) bool {
	for _, svc := range project.Services {
		if svc.Build != nil {
			return true
		}
	}
	return false
}
