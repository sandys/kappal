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
	// Get the directory containing the compose file for context
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}

	// Build options
	opts := []cli.ProjectOptionsFn{
		cli.WithOsEnv,
		cli.WithDotEnv,
		cli.WithWorkingDirectory(dir),
	}

	if projectName != "" {
		opts = append(opts, cli.WithName(projectName))
	} else {
		// Use directory name as project name
		absDir, err := filepath.Abs(dir)
		if err == nil {
			opts = append(opts, cli.WithName(filepath.Base(absDir)))
		}
	}

	options, err := cli.NewProjectOptions([]string{path}, opts...)
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
