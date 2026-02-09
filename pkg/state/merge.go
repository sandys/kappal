package state

import (
	"fmt"
	"sort"

	"github.com/compose-spec/compose-go/v2/types"
)

// MergeCompose combines discovered live state with compose file definitions.
// Services in compose but not in K8s get status "missing" (or "unavailable" if !k8sAvailable).
// Returns an ordered service list matching compose file order (alphabetical).
func MergeCompose(discovered *State, project *types.Project) []ServiceInfo {
	// Build ordered service names from compose file
	serviceNames := make([]string, 0, len(project.Services))
	for name := range project.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	var result []ServiceInfo
	for _, name := range serviceNames {
		composeSvc := project.Services[name]

		// Skip services with profiles (not activated by default)
		if len(composeSvc.Profiles) > 0 {
			continue
		}

		composeKind := "Deployment"
		if composeSvc.Restart == "no" {
			composeKind = "Job"
		}

		image := composeSvc.Image
		if image == "" && composeSvc.Build != nil {
			image = fmt.Sprintf("%s-%s:latest", project.Name, name)
		}

		// Extract healthcheck from compose definition
		var hc *HealthCheck
		if composeSvc.HealthCheck != nil && !composeSvc.HealthCheck.Disable && len(composeSvc.HealthCheck.Test) > 0 {
			hc = &HealthCheck{
				Test: composeSvc.HealthCheck.Test,
			}
			if composeSvc.HealthCheck.Interval != nil {
				hc.Interval = composeSvc.HealthCheck.Interval.String()
			}
			if composeSvc.HealthCheck.Timeout != nil {
				hc.Timeout = composeSvc.HealthCheck.Timeout.String()
			}
			if composeSvc.HealthCheck.Retries != nil {
				hc.Retries = int(*composeSvc.HealthCheck.Retries)
			}
			if composeSvc.HealthCheck.StartPeriod != nil {
				hc.StartPeriod = composeSvc.HealthCheck.StartPeriod.String()
			}
		}

		if !discovered.K8sAvailable {
			result = append(result, ServiceInfo{
				Name:        name,
				Kind:        composeKind,
				Image:       image,
				Status:      "unavailable",
				Pods:        []PodInfo{},
				HealthCheck: hc,
			})
			continue
		}

		if svcInfo, ok := discovered.Services[name]; ok {
			info := *svcInfo
			info.HealthCheck = hc
			result = append(result, info)
		} else {
			result = append(result, ServiceInfo{
				Name:        name,
				Kind:        composeKind,
				Image:       image,
				Status:      "missing",
				Pods:        []PodInfo{},
				HealthCheck: hc,
			})
		}
	}

	return result
}
