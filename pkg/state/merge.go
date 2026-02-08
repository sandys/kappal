package state

import (
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

		if !discovered.K8sAvailable {
			result = append(result, ServiceInfo{
				Name:   name,
				Kind:   composeKind,
				Image:  composeSvc.Image,
				Status: "unavailable",
				Pods:   []PodInfo{},
			})
			continue
		}

		if svcInfo, ok := discovered.Services[name]; ok {
			result = append(result, *svcInfo)
		} else {
			result = append(result, ServiceInfo{
				Name:   name,
				Kind:   composeKind,
				Image:  composeSvc.Image,
				Status: "missing",
				Pods:   []PodInfo{},
			})
		}
	}

	return result
}
