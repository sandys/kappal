package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// InitSpec defines what this init container should wait for.
type InitSpec struct {
	Namespace            string   `json:"namespace"`
	WaitForJobs          []string `json:"waitForJobs"`
	WaitForServices      []string `json:"waitForServices"`
	PrepareWritablePaths []string `json:"prepareWritablePaths,omitempty"`
}

func main() {
	specJSON := os.Getenv("KAPPAL_INIT_SPEC")
	if specJSON == "" {
		fmt.Println("KAPPAL_INIT_SPEC not set, nothing to wait for")
		os.Exit(0)
	}

	var spec InitSpec
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse KAPPAL_INIT_SPEC: %v\n", err)
		os.Exit(1)
	}

	if len(spec.WaitForJobs) == 0 && len(spec.WaitForServices) == 0 && len(spec.PrepareWritablePaths) == 0 {
		fmt.Println("No jobs/services to wait for and no writable paths to prepare")
		os.Exit(0)
	}

	if len(spec.PrepareWritablePaths) > 0 {
		fmt.Printf("Preparing writable paths: %v\n", spec.PrepareWritablePaths)
		if err := prepareWritablePaths(spec.PrepareWritablePaths); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to prepare writable paths: %v\n", err)
			os.Exit(1)
		}
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get in-cluster config: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Wait for all jobs to complete
	if len(spec.WaitForJobs) > 0 {
		fmt.Printf("Waiting for jobs to complete: %v\n", spec.WaitForJobs)
		if err := waitForJobs(ctx, clientset, spec.Namespace, spec.WaitForJobs); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Println("All dependency jobs completed successfully")
	}

	// Wait for all services to become ready
	if len(spec.WaitForServices) > 0 {
		fmt.Printf("Waiting for services to become ready: %v\n", spec.WaitForServices)
		if err := waitForServices(ctx, clientset, spec.Namespace, spec.WaitForServices); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		fmt.Println("All dependency services are ready")
	}
}

func waitForJobs(ctx context.Context, clientset *kubernetes.Clientset, namespace string, jobs []string) error {
	for {
		allDone := true
		for _, jobName := range jobs {
			job, err := clientset.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
			if err != nil {
				fmt.Printf("Waiting for job %s: %v\n", jobName, err)
				allDone = false
				break
			}
			if isJobFailed(job) {
				return fmt.Errorf("job %s failed", jobName)
			}
			if !isJobComplete(job) {
				fmt.Printf("Job %s not yet complete (succeeded=%d, failed=%d)\n",
					jobName, job.Status.Succeeded, job.Status.Failed)
				allDone = false
				break
			}
		}

		if allDone {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for jobs to complete")
		case <-time.After(3 * time.Second):
		}
	}
}

func waitForServices(ctx context.Context, clientset *kubernetes.Clientset, namespace string, services []string) error {
	for {
		allReady := true
		for _, svcName := range services {
			ready, err := isServiceReady(ctx, clientset, namespace, svcName)
			if err != nil {
				fmt.Printf("Waiting for service %s: %v\n", svcName, err)
				allReady = false
				break
			}
			if !ready {
				fmt.Printf("Service %s not yet ready\n", svcName)
				allReady = false
				break
			}
		}

		if allReady {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for services to become ready")
		case <-time.After(3 * time.Second):
		}
	}
}

// isServiceReady checks if at least one pod for the service has Ready=True condition.
func isServiceReady(ctx context.Context, clientset *kubernetes.Clientset, namespace, serviceName string) (bool, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("kappal.io/service=%s", serviceName),
	})
	if err != nil {
		return false, err
	}

	for _, pod := range pods.Items {
		if isPodReady(&pod) {
			return true, nil
		}
	}
	return false, nil
}

func isPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func isJobComplete(job *batchv1.Job) bool {
	return job.Status.Succeeded >= 1
}

func isJobFailed(job *batchv1.Job) bool {
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == "True" {
			return true
		}
	}
	return false
}

// prepareWritablePaths ensures bind-mounted paths are writable by non-root workloads.
// This mirrors common docker-compose behavior where bind targets are writable by app users.
func prepareWritablePaths(paths []string) error {
	for _, rawPath := range paths {
		if rawPath == "" {
			continue
		}
		path := filepath.Clean(rawPath)
		if !filepath.IsAbs(path) {
			return fmt.Errorf("path must be absolute: %q", rawPath)
		}
		if path == "/" {
			return fmt.Errorf("refusing unsafe chmod on root path")
		}

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if err := os.MkdirAll(path, 0777); err != nil {
					return fmt.Errorf("create directory %s: %w", path, err)
				}
				if err := os.Chmod(path, 0777); err != nil {
					return fmt.Errorf("chmod directory %s: %w", path, err)
				}
				continue
			}
			return fmt.Errorf("stat %s: %w", path, err)
		}

		if info.IsDir() {
			if err := os.Chmod(path, 0777); err != nil {
				return fmt.Errorf("chmod directory %s: %w", path, err)
			}
			continue
		}

		if err := os.Chmod(path, 0666); err != nil {
			return fmt.Errorf("chmod file %s: %w", path, err)
		}
	}
	return nil
}
