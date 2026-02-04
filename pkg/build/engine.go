package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Engine handles image building
type Engine struct {
	projectName string
}

// NewEngine creates a new build engine
func NewEngine(projectName string) *Engine {
	return &Engine{projectName: projectName}
}

// Build builds an image for a service
func (e *Engine) Build(ctx context.Context, serviceName, contextDir, dockerfile string) (string, error) {
	imageName := fmt.Sprintf("%s-%s:latest", e.projectName, serviceName)

	args := []string{"build", "-t", imageName}

	if dockerfile != "" {
		args = append(args, "-f", dockerfile)
	}

	args = append(args, contextDir)

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build failed: %w", err)
	}

	return imageName, nil
}

// BuildWithBuildKit builds using BuildKit (for future enhancement)
func (e *Engine) BuildWithBuildKit(ctx context.Context, serviceName, contextDir, dockerfile string) (string, error) {
	// For now, use standard docker build
	// TODO: Integrate BuildKit client directly for in-cluster builds
	return e.Build(ctx, serviceName, contextDir, dockerfile)
}
