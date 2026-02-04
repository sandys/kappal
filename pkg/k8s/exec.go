package k8s

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecOptions configures the exec operation
type ExecOptions struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	TTY         bool
	Interactive bool
	Index       int // Index of pod if multiple replicas
}

// GetConfig returns a REST config from the kubeconfig path
func GetConfig(kubeconfigPath string) (*rest.Config, error) {
	return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
}

// Exec executes a command in a service's pod
func (c *Client) Exec(ctx context.Context, namespace, serviceName string, command []string, opts ExecOptions) error {
	// Find pods for this service
	pods, err := c.ListPods(ctx, namespace, fmt.Sprintf("kappal.io/service=%s", serviceName))
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no running container for service %s", serviceName)
	}

	// Select pod by index (default to first)
	podIndex := opts.Index
	if podIndex < 0 || podIndex >= len(pods.Items) {
		podIndex = 0
	}
	pod := pods.Items[podIndex]

	// Check if pod is running
	if pod.Status.Phase != corev1.PodRunning {
		return fmt.Errorf("pod %s is not running (status: %s)", pod.Name, pod.Status.Phase)
	}

	return c.execInPod(ctx, namespace, pod.Name, command, opts)
}

// execInPod executes a command in a specific pod
func (c *Client) execInPod(ctx context.Context, namespace, podName string, command []string, opts ExecOptions) error {
	// Create exec request
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: command,
			Stdin:   opts.Interactive || opts.Stdin != nil,
			Stdout:  true,
			Stderr:  true,
			TTY:     opts.TTY,
		}, scheme.ParameterCodec)

	// Get REST config from client
	config := c.RESTConfig()
	if config == nil {
		return fmt.Errorf("REST config not available")
	}

	// Create SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	// Stream options
	streamOpts := remotecommand.StreamOptions{
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
		Tty:    opts.TTY,
	}

	if opts.Interactive || opts.Stdin != nil {
		streamOpts.Stdin = opts.Stdin
	}

	// Execute command
	return exec.StreamWithContext(ctx, streamOpts)
}
