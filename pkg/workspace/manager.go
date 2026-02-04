package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Workspace manages the .kappal directory structure
type Workspace struct {
	Root        string
	EnvDir      string
	LibDir      string
	RuntimeDir  string
	ManifestDir string
}

// TankaSpec represents the Tanka environment spec
type TankaSpec struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   TankaMetadata     `json:"metadata"`
	Spec       TankaSpecInner    `json:"spec"`
}

type TankaMetadata struct {
	Name string `json:"name"`
}

type TankaSpecInner struct {
	APIServer        string `json:"apiServer"`
	Namespace        string `json:"namespace"`
	ResourceDefaults *ResourceDefaults `json:"resourceDefaults,omitempty"`
}

type ResourceDefaults struct {
	Annotations map[string]string `json:"annotations,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// New creates a new workspace at the given path
func New(root string) (*Workspace, error) {
	ws := &Workspace{
		Root:        root,
		EnvDir:      filepath.Join(root, "environments", "default"),
		LibDir:      filepath.Join(root, "lib"),
		RuntimeDir:  filepath.Join(root, "runtime"),
		ManifestDir: filepath.Join(root, "manifests"),
	}

	// Create directories
	dirs := []string{
		ws.EnvDir,
		ws.LibDir,
		ws.RuntimeDir,
		ws.ManifestDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Write .gitignore
	gitignore := `# Kappal runtime data
runtime/
*.log

# Keep environments and lib
!environments/
!lib/
`
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignore), 0644); err != nil {
		return nil, fmt.Errorf("failed to write .gitignore: %w", err)
	}

	return ws, nil
}

// Open opens an existing workspace
func Open(root string) (*Workspace, error) {
	ws := &Workspace{
		Root:        root,
		EnvDir:      filepath.Join(root, "environments", "default"),
		LibDir:      filepath.Join(root, "lib"),
		RuntimeDir:  filepath.Join(root, "runtime"),
		ManifestDir: filepath.Join(root, "manifests"),
	}

	// Check if workspace exists
	if _, err := os.Stat(ws.EnvDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("workspace not found at %s", root)
	}

	return ws, nil
}

// WriteSpec writes the compose spec as JSON
func (w *Workspace) WriteSpec(spec interface{}) error {
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal spec: %w", err)
	}

	return os.WriteFile(filepath.Join(w.EnvDir, "spec.json"), data, 0644)
}

// WriteMainJsonnet writes the main.jsonnet file
func (w *Workspace) WriteMainJsonnet(content string) error {
	return os.WriteFile(filepath.Join(w.EnvDir, "main.jsonnet"), []byte(content), 0644)
}

// WriteLibsonnet writes a library file to the lib directory
func (w *Workspace) WriteLibsonnet(name string, content string) error {
	return os.WriteFile(filepath.Join(w.LibDir, name), []byte(content), 0644)
}

// WriteTankaSpec writes the Tanka environment spec
func (w *Workspace) WriteTankaSpec(apiServer, namespace string) error {
	spec := TankaSpec{
		APIVersion: "tanka.dev/v1alpha1",
		Kind:       "Environment",
		Metadata: TankaMetadata{
			Name: "environments/default",
		},
		Spec: TankaSpecInner{
			APIServer: apiServer,
			Namespace: namespace,
		},
	}

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tanka spec: %w", err)
	}

	return os.WriteFile(filepath.Join(w.EnvDir, "spec.json"), data, 0644)
}

// WriteJsonnetfile writes the jsonnetfile.json for dependencies
func (w *Workspace) WriteJsonnetfile() error {
	jf := map[string]interface{}{
		"version": 1,
		"dependencies": []map[string]interface{}{
			{
				"source": map[string]interface{}{
					"git": map[string]string{
						"remote": "https://github.com/jsonnet-libs/k8s-libsonnet",
						"subdir": "1.29",
					},
				},
				"version": "main",
			},
		},
		"legacyImports": true,
	}

	data, err := json.MarshalIndent(jf, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(w.Root, "jsonnetfile.json"), data, 0644)
}

// WriteManifest writes a Kubernetes manifest to the manifest directory
func (w *Workspace) WriteManifest(name string, content []byte) error {
	return os.WriteFile(filepath.Join(w.ManifestDir, name), content, 0644)
}

// GetManifestDir returns the manifest directory path
func (w *Workspace) GetManifestDir() string {
	return w.ManifestDir
}

// GetRuntimeDir returns the runtime directory path
func (w *Workspace) GetRuntimeDir() string {
	return w.RuntimeDir
}

// GetKubeconfigPath returns the path to the kubeconfig file
func (w *Workspace) GetKubeconfigPath() string {
	return filepath.Join(w.RuntimeDir, "kubeconfig.yaml")
}

// CleanRuntime removes the runtime directory
func (w *Workspace) CleanRuntime() error {
	return os.RemoveAll(w.RuntimeDir)
}

// CleanManifests removes all manifests
func (w *Workspace) CleanManifests() error {
	return os.RemoveAll(w.ManifestDir)
}
