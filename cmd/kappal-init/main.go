package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// InitSpec defines what this init container should wait for.
type InitSpec struct {
	Namespace   string   `json:"namespace"`
	WaitForJobs []string `json:"waitForJobs"`
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

	if len(spec.WaitForJobs) == 0 {
		fmt.Println("No jobs to wait for")
		os.Exit(0)
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

	fmt.Printf("Waiting for jobs to complete: %v\n", spec.WaitForJobs)

	for {
		allDone := true
		for _, jobName := range spec.WaitForJobs {
			job, err := clientset.BatchV1().Jobs(spec.Namespace).Get(ctx, jobName, metav1.GetOptions{})
			if err != nil {
				fmt.Printf("Waiting for job %s: %v\n", jobName, err)
				allDone = false
				break
			}
			if isJobFailed(job) {
				fmt.Fprintf(os.Stderr, "Job %s failed\n", jobName)
				os.Exit(1)
			}
			if !isJobComplete(job) {
				fmt.Printf("Job %s not yet complete (succeeded=%d, failed=%d)\n",
					jobName, job.Status.Succeeded, job.Status.Failed)
				allDone = false
				break
			}
		}

		if allDone {
			fmt.Println("All dependency jobs completed successfully")
			os.Exit(0)
		}

		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "Timeout waiting for jobs to complete\n")
			os.Exit(1)
		case <-time.After(3 * time.Second):
		}
	}
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
