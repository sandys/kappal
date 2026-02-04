package k8s

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/compose-spec/compose-go/v2/types"
	corev1 "k8s.io/api/core/v1"
)

// LogOptions configures log streaming
type LogOptions struct {
	Follow    bool
	TailLines int64
	Services  []string
}

// StreamLogs streams logs from services in a project
func (c *Client) StreamLogs(ctx context.Context, project *types.Project, opts LogOptions, out io.Writer) error {
	services := opts.Services
	if len(services) == 0 {
		for _, svc := range project.Services {
			services = append(services, svc.Name)
		}
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(services))

	for _, svcName := range services {
		wg.Add(1)
		go func(service string) {
			defer wg.Done()
			if err := c.streamServiceLogs(ctx, project.Name, service, opts, out); err != nil {
				errChan <- fmt.Errorf("%s: %w", service, err)
			}
		}(svcName)
	}

	// Wait for all streams to finish
	go func() {
		wg.Wait()
		close(errChan)
	}()

	// If not following, wait for completion
	if !opts.Follow {
		wg.Wait()
	} else {
		// Block until context is cancelled
		<-ctx.Done()
	}

	// Check for errors
	for err := range errChan {
		return err
	}

	return nil
}

func (c *Client) streamServiceLogs(ctx context.Context, namespace, serviceName string, opts LogOptions, out io.Writer) error {
	// Find pods for this service
	pods, err := c.ListPods(ctx, namespace, fmt.Sprintf("kappal.io/service=%s", serviceName))
	if err != nil {
		return err
	}

	if len(pods.Items) == 0 {
		fmt.Fprintf(out, "%s | No pods found\n", serviceName)
		return nil
	}

	var wg sync.WaitGroup
	for _, pod := range pods.Items {
		wg.Add(1)
		go func(podName string) {
			defer wg.Done()
			c.streamPodLogs(ctx, namespace, podName, serviceName, opts, out)
		}(pod.Name)
	}

	wg.Wait()
	return nil
}

func (c *Client) streamPodLogs(ctx context.Context, namespace, podName, serviceName string, opts LogOptions, out io.Writer) {
	logOpts := &corev1.PodLogOptions{
		Follow: opts.Follow,
	}

	if opts.TailLines > 0 {
		logOpts.TailLines = &opts.TailLines
	}

	stream, err := c.GetPodLogs(ctx, namespace, podName, logOpts)
	if err != nil {
		fmt.Fprintf(out, "%s | Error: %v\n", serviceName, err)
		return
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			fmt.Fprintf(out, "%s | %s\n", serviceName, scanner.Text())
		}
	}
}
