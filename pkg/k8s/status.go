package k8s

import (
	"context"
	"fmt"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	corev1 "k8s.io/api/core/v1"
)

// ServiceStatus represents the status of a compose service
type ServiceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Ports  string `json:"ports"`
	Ready  int    `json:"ready"`
	Total  int    `json:"total"`
}

// GetServiceStatuses returns the status of all services in a project
func (c *Client) GetServiceStatuses(ctx context.Context, project *types.Project) ([]ServiceStatus, error) {
	var statuses []ServiceStatus

	for _, svc := range project.Services {
		status := ServiceStatus{
			Name:   svc.Name,
			Status: "Not Found",
		}

		// Get pods for this service
		pods, err := c.ListPods(ctx, project.Name, fmt.Sprintf("kappal.io/service=%s", svc.Name))
		if err != nil {
			statuses = append(statuses, status)
			continue
		}

		if len(pods.Items) == 0 {
			status.Status = "Not Running"
			statuses = append(statuses, status)
			continue
		}

		// Count ready pods
		readyCount := 0
		totalCount := len(pods.Items)
		var containerStatuses []string

		for _, pod := range pods.Items {
			podStatus := mapPodPhase(pod.Status.Phase)

			// Check container status for more detail
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					podStatus = mapWaitingReason(cs.State.Waiting.Reason)
				} else if cs.State.Terminated != nil {
					podStatus = fmt.Sprintf("Exited (%d)", cs.State.Terminated.ExitCode)
				} else if cs.Ready {
					readyCount++
				}
			}

			containerStatuses = append(containerStatuses, podStatus)
		}

		status.Ready = readyCount
		status.Total = totalCount

		// Determine overall status
		if readyCount == totalCount && totalCount > 0 {
			status.Status = "Up"
		} else if len(containerStatuses) > 0 {
			status.Status = containerStatuses[0]
		}

		// Format ports
		var ports []string
		for _, p := range svc.Ports {
			protocol := strings.ToLower(p.Protocol)
			if protocol == "" {
				protocol = "tcp"
			}
			ports = append(ports, fmt.Sprintf("%s->%d/%s", p.Published, p.Target, protocol))
		}
		status.Ports = strings.Join(ports, ", ")

		statuses = append(statuses, status)
	}

	return statuses, nil
}

// mapPodPhase maps Kubernetes pod phase to Compose status
func mapPodPhase(phase corev1.PodPhase) string {
	switch phase {
	case corev1.PodRunning:
		return "Up"
	case corev1.PodPending:
		return "Starting"
	case corev1.PodSucceeded:
		return "Exited (0)"
	case corev1.PodFailed:
		return "Exited (1)"
	case corev1.PodUnknown:
		return "Error"
	default:
		return string(phase)
	}
}

// mapWaitingReason maps Kubernetes waiting reasons to Compose status
func mapWaitingReason(reason string) string {
	switch reason {
	case "CrashLoopBackOff":
		return "Restarting"
	case "ImagePullBackOff", "ErrImagePull":
		return "Error (image)"
	case "ContainerCreating":
		return "Starting"
	case "PodInitializing":
		return "Starting"
	default:
		return reason
	}
}
