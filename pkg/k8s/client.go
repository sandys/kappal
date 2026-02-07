package k8s

import (
	"context"
	"fmt"
	"io"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps the Kubernetes client-go clientset
type Client struct {
	clientset  *kubernetes.Clientset
	restConfig *rest.Config
}

// NewClient creates a new Kubernetes client from a kubeconfig file
func NewClient(kubeconfigPath string) (*Client, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return &Client{clientset: clientset, restConfig: config}, nil
}

// RESTConfig returns the REST config for the client
func (c *Client) RESTConfig() *rest.Config {
	return c.restConfig
}

// Clientset returns the underlying kubernetes clientset
func (c *Client) Clientset() *kubernetes.Clientset {
	return c.clientset
}

// CheckConnection verifies the connection to the cluster
func (c *Client) CheckConnection(ctx context.Context) error {
	_, err := c.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	return err
}

// ListPods returns pods matching the given label selector in a namespace
func (c *Client) ListPods(ctx context.Context, namespace, labelSelector string) (*corev1.PodList, error) {
	return c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
}

// GetPod returns a specific pod
func (c *Client) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	return c.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
}

// WatchPods returns a watch interface for pods matching the label selector
func (c *Client) WatchPods(ctx context.Context, namespace, labelSelector string) (watch.Interface, error) {
	return c.clientset.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
}

// GetPodLogs returns a ReadCloser for streaming pod logs
func (c *Client) GetPodLogs(ctx context.Context, namespace, podName string, opts *corev1.PodLogOptions) (io.ReadCloser, error) {
	req := c.clientset.CoreV1().Pods(namespace).GetLogs(podName, opts)
	return req.Stream(ctx)
}

// NamespaceExists checks if a namespace exists
func (c *Client) NamespaceExists(ctx context.Context, name string) (bool, error) {
	_, err := c.clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, nil
	}
	return true, nil
}

// WaitForPodsReady waits for all pods matching the selector to be ready
func (c *Client) WaitForPodsReady(ctx context.Context, namespace, labelSelector string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		pods, err := c.ListPods(ctx, namespace, labelSelector)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		if len(pods.Items) == 0 {
			time.Sleep(2 * time.Second)
			continue
		}

		allReady := true
		for _, pod := range pods.Items {
			switch pod.Status.Phase {
			case corev1.PodSucceeded:
				continue // Job completed successfully, counts as ready
			case corev1.PodRunning:
				// Check Ready condition
				for _, cond := range pod.Status.Conditions {
					if cond.Type == corev1.PodReady && cond.Status != corev1.ConditionTrue {
						allReady = false
						break
					}
				}
			default:
				allReady = false
			}
			if !allReady {
				break
			}
		}

		if allReady {
			return nil
		}

		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("timeout waiting for pods to be ready")
}

// DeleteJobs deletes all Jobs in a namespace with the kappal project label.
// Jobs are immutable in K8s, so they must be deleted before re-applying.
func (c *Client) DeleteJobs(ctx context.Context, namespace string) error {
	propagation := metav1.DeletePropagationBackground
	return c.clientset.BatchV1().Jobs(namespace).DeleteCollection(ctx,
		metav1.DeleteOptions{PropagationPolicy: &propagation},
		metav1.ListOptions{LabelSelector: "kappal.io/project=" + namespace},
	)
}

// GetNodes returns the list of nodes in the cluster
func (c *Client) GetNodes(ctx context.Context) (*corev1.NodeList, error) {
	return c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
}

// ListServices returns services matching the given label selector in a namespace
func (c *Client) ListServices(ctx context.Context, namespace, labelSelector string) (*corev1.ServiceList, error) {
	return c.clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
}

// ListDeployments returns deployments matching the given label selector in a namespace
func (c *Client) ListDeployments(ctx context.Context, namespace, labelSelector string) (*appsv1.DeploymentList, error) {
	return c.clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
}

// ListJobs returns jobs matching the given label selector in a namespace
func (c *Client) ListJobs(ctx context.Context, namespace, labelSelector string) (*batchv1.JobList, error) {
	return c.clientset.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
}
