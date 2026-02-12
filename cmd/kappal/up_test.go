package main

import (
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestShouldLoadInitImage(t *testing.T) {
	t.Run("writable bind mount", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"app": {
					Name: "app",
					Volumes: []types.ServiceVolumeConfig{
						{Type: "bind", Source: "./data", Target: "/data"},
					},
				},
			},
		}
		if !shouldLoadInitImage(project) {
			t.Fatal("expected init image load for writable bind mount")
		}
	})

	t.Run("read-only bind mount only", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"app": {
					Name: "app",
					Volumes: []types.ServiceVolumeConfig{
						{Type: "bind", Source: "./data", Target: "/data", ReadOnly: true},
					},
				},
			},
		}
		if shouldLoadInitImage(project) {
			t.Fatal("did not expect init image load for read-only bind mount only")
		}
	})

	t.Run("job dependency requires init image", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"setup": {
					Name:    "setup",
					Restart: "no",
				},
				"app": {
					Name: "app",
					DependsOn: types.DependsOnConfig{
						"setup": {Condition: "service_completed_successfully"},
					},
				},
			},
		}
		if !shouldLoadInitImage(project) {
			t.Fatal("expected init image load for service_completed_successfully dependency")
		}
	})

	t.Run("healthy dependency requires init image", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"db": {
					Name:    "db",
					Restart: "unless-stopped",
				},
				"app": {
					Name: "app",
					DependsOn: types.DependsOnConfig{
						"db": {Condition: "service_healthy"},
					},
				},
			},
		}
		if !shouldLoadInitImage(project) {
			t.Fatal("expected init image load for service_healthy dependency")
		}
	})

	t.Run("dependency on profiled service is ignored", func(t *testing.T) {
		project := &types.Project{
			Services: types.Services{
				"db": {
					Name:     "db",
					Restart:  "unless-stopped",
					Profiles: []string{"manual"},
				},
				"app": {
					Name: "app",
					DependsOn: types.DependsOnConfig{
						"db": {Condition: "service_healthy"},
					},
				},
			},
		}
		if shouldLoadInitImage(project) {
			t.Fatal("did not expect init image load for dependency on profiled service")
		}
	})
}

func TestAnalyzeCompatibilityNotes(t *testing.T) {
	project := &types.Project{
		Services: types.Services{
			"db": {
				Name:     "db",
				Profiles: []string{"manual"},
			},
			"app": {
				Name: "app",
				Volumes: []types.ServiceVolumeConfig{
					{Type: "bind", Source: "./data", Target: "/data"},
				},
				DependsOn: types.DependsOnConfig{
					"db":      {Condition: "service_healthy"},
					"missing": {Condition: "service_started"},
				},
			},
		},
	}

	report := analyzeCompatibility(project)
	if !report.NeedInitImage {
		t.Fatal("expected NeedInitImage=true due to writable bind mount")
	}

	joined := strings.Join(report.Notes, "\n")
	if !strings.Contains(joined, `service "app" uses writable bind mounts`) {
		t.Fatalf("expected writable bind mount note, got: %s", joined)
	}
	if !strings.Contains(joined, `service "app" depends_on profiled service "db"`) {
		t.Fatalf("expected profiled dependency note, got: %s", joined)
	}
	if !strings.Contains(joined, `service "app" depends_on "missing"`) {
		t.Fatalf("expected missing dependency note, got: %s", joined)
	}
}
