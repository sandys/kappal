package build

import (
	"context"
	"fmt"

	"github.com/kappal-app/kappal/pkg/docker"
)

// Engine handles image building
type Engine struct {
	projectName string
	docker      *docker.Client
}

// NewEngine creates a new build engine
func NewEngine(projectName string) (*Engine, error) {
	dockerClient, err := docker.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	return &Engine{
		projectName: projectName,
		docker:      dockerClient,
	}, nil
}

// Close closes the Docker client
func (e *Engine) Close() error {
	if e.docker != nil {
		return e.docker.Close()
	}
	return nil
}

// Build builds an image for a service
func (e *Engine) Build(ctx context.Context, serviceName, contextDir, dockerfile string) (string, error) {
	imageName := fmt.Sprintf("%s-%s:latest", e.projectName, serviceName)

	// If no dockerfile specified, use default "Dockerfile"
	dockerfilePath := dockerfile
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}

	if err := e.docker.ImageBuild(ctx, contextDir, dockerfilePath, imageName); err != nil {
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
