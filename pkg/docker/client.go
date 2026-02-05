package docker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/stdcopy"
)

// Client wraps the Docker SDK client
type Client struct {
	cli *client.Client
}

// NewClient creates a Docker client from environment
func NewClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	return &Client{cli: cli}, nil
}

// Close closes the Docker client
func (c *Client) Close() error {
	return c.cli.Close()
}

// ContainerState returns (exists, running, error) for a container
func (c *Client) ContainerState(ctx context.Context, name string) (exists bool, running bool, err error) {
	inspect, err := c.cli.ContainerInspect(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("failed to inspect container %s: %w", name, err)
	}
	return true, inspect.State.Running, nil
}

// ContainerRemove removes a container (force). Idempotent - returns nil if container doesn't exist.
func (c *Client) ContainerRemove(ctx context.Context, name string) error {
	err := c.cli.ContainerRemove(ctx, name, types.ContainerRemoveOptions{Force: true})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil // Idempotent
		}
		return fmt.Errorf("failed to remove container %s: %w", name, err)
	}
	return nil
}

// ContainerStop stops a container. Idempotent - returns nil if container doesn't exist or is already stopped.
func (c *Client) ContainerStop(ctx context.Context, name string, timeout time.Duration) error {
	timeoutSec := int(timeout.Seconds())
	err := c.cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeoutSec})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil // Idempotent
		}
		return fmt.Errorf("failed to stop container %s: %w", name, err)
	}
	return nil
}

// ContainerCreate creates a container without starting it
func (c *Client) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, name string) (string, error) {
	resp, err := c.cli.ContainerCreate(ctx, config, hostConfig, nil, nil, name)
	if err != nil {
		return "", fmt.Errorf("failed to create container %s: %w", name, err)
	}
	return resp.ID, nil
}

// ContainerStart starts an existing container
func (c *Client) ContainerStart(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerStart(ctx, containerID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start container %s: %w", containerID, err)
	}
	return nil
}

// ContainerRun creates and starts a container (like docker run -d)
func (c *Client) ContainerRun(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, name string) error {
	containerID, err := c.ContainerCreate(ctx, config, hostConfig, name)
	if err != nil {
		return err
	}
	return c.ContainerStart(ctx, containerID)
}

// ContainerExec runs a command in a container and returns output
func (c *Client) ContainerExec(ctx context.Context, name string, cmd []string) ([]byte, error) {
	execConfig := types.ExecConfig{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execResp, err := c.cli.ContainerExecCreate(ctx, name, execConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create exec in container %s: %w", name, err)
	}

	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, types.ExecStartCheck{})
	if err != nil {
		return nil, fmt.Errorf("failed to attach to exec in container %s: %w", name, err)
	}
	defer attachResp.Close()

	// Read the output - demux stdout and stderr
	var stdout, stderr io.Writer
	stderr = io.Discard

	// Create a buffer to capture stdout
	var buf []byte
	stdout = &byteWriter{buf: &buf}

	_, err = stdcopy.StdCopy(stdout, stderr, attachResp.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read exec output: %w", err)
	}

	return buf, nil
}

// byteWriter is a simple io.Writer that appends to a byte slice
type byteWriter struct {
	buf *[]byte
}

func (w *byteWriter) Write(p []byte) (n int, err error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

// ContainerExecStream runs a command in a container with streaming I/O
func (c *Client) ContainerExecStream(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	execConfig := types.ExecConfig{
		Cmd:          cmd,
		AttachStdin:  stdin != nil,
		AttachStdout: stdout != nil,
		AttachStderr: stderr != nil,
	}
	execResp, err := c.cli.ContainerExecCreate(ctx, name, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec in container %s: %w", name, err)
	}

	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, types.ExecStartCheck{})
	if err != nil {
		return fmt.Errorf("failed to attach to exec in container %s: %w", name, err)
	}
	defer attachResp.Close()

	// Handle stdin in a goroutine
	if stdin != nil {
		go func() {
			_, _ = io.Copy(attachResp.Conn, stdin)
			_ = attachResp.CloseWrite()
		}()
	}

	// Copy stdout/stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	_, err = stdcopy.StdCopy(stdout, stderr, attachResp.Reader)
	if err != nil {
		return fmt.Errorf("failed to read exec output: %w", err)
	}

	// Check exec exit status - commands can fail even if streaming succeeded
	inspectResp, err := c.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return fmt.Errorf("failed to inspect exec status: %w", err)
	}
	if inspectResp.ExitCode != 0 {
		return fmt.Errorf("command exited with code %d", inspectResp.ExitCode)
	}

	return nil
}

// readDockerignore reads .dockerignore file and returns exclude patterns
func readDockerignore(contextDir string) ([]string, error) {
	dockerignorePath := filepath.Join(contextDir, ".dockerignore")
	f, err := os.Open(dockerignorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No .dockerignore, no excludes
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var excludes []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		excludes = append(excludes, line)
	}
	return excludes, scanner.Err()
}

// ImageBuild builds an image from context directory
func (c *Client) ImageBuild(ctx context.Context, contextDir, dockerfile, imageName string, buildArgs map[string]*string) error {
	// Read .dockerignore patterns
	excludes, err := readDockerignore(contextDir)
	if err != nil {
		return fmt.Errorf("failed to read .dockerignore: %w", err)
	}

	// Force-include the Dockerfile even if .dockerignore would exclude it.
	// This handles cases like "Dockerfile*" in .dockerignore.
	// Negation patterns (starting with !) override previous exclusions.
	// Normalize dockerfile path: clean, convert to slashes, trim leading ./
	normalizedDockerfile := filepath.ToSlash(filepath.Clean(dockerfile))
	normalizedDockerfile = strings.TrimPrefix(normalizedDockerfile, "./")
	excludes = append(excludes, "!"+normalizedDockerfile)

	// Tar the context directory with exclusions
	tarCtx, err := archive.TarWithOptions(contextDir, &archive.TarOptions{
		ExcludePatterns: excludes,
	})
	if err != nil {
		return fmt.Errorf("failed to create build context tar: %w", err)
	}
	defer func() { _ = tarCtx.Close() }()

	opts := types.ImageBuildOptions{
		Tags:       []string{imageName},
		Dockerfile: dockerfile,
		Remove:     true,
		BuildArgs:  buildArgs,
	}

	resp, err := c.cli.ImageBuild(ctx, tarCtx, opts)
	if err != nil {
		return fmt.Errorf("failed to build image %s: %w", imageName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Stream build output and check for errors
	err = jsonmessage.DisplayJSONMessagesStream(resp.Body, os.Stdout, 0, false, nil)
	if err != nil {
		return fmt.Errorf("build failed for image %s: %w", imageName, err)
	}

	return nil
}

// ImageSave exports an image as tar stream
func (c *Client) ImageSave(ctx context.Context, imageName string) (io.ReadCloser, error) {
	reader, err := c.cli.ImageSave(ctx, []string{imageName})
	if err != nil {
		return nil, fmt.Errorf("failed to save image %s: %w", imageName, err)
	}
	return reader, nil
}

// ImageLoad loads an image from a tar stream
func (c *Client) ImageLoad(ctx context.Context, input io.Reader) error {
	resp, err := c.cli.ImageLoad(ctx, input, true)
	if err != nil {
		return fmt.Errorf("failed to load image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Drain the response body
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// VolumeRemove removes a volume. Idempotent - returns nil if volume doesn't exist.
func (c *Client) VolumeRemove(ctx context.Context, name string) error {
	err := c.cli.VolumeRemove(ctx, name, true)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil // Idempotent
		}
		return fmt.Errorf("failed to remove volume %s: %w", name, err)
	}
	return nil
}

// VolumeCreate creates a volume
func (c *Client) VolumeCreate(ctx context.Context, name string) error {
	_, err := c.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name})
	if err != nil {
		return fmt.Errorf("failed to create volume %s: %w", name, err)
	}
	return nil
}

// ImagePull pulls an image from a registry
func (c *Client) ImagePull(ctx context.Context, imageName string) error {
	reader, err := c.cli.ImagePull(ctx, imageName, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", imageName, err)
	}
	defer func() { _ = reader.Close() }()

	// Stream pull output
	err = jsonmessage.DisplayJSONMessagesStream(reader, os.Stdout, 0, false, nil)
	if err != nil {
		return fmt.Errorf("pull failed for image %s: %w", imageName, err)
	}

	return nil
}
