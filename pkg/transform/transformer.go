package transform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/kappal-app/kappal/pkg/workspace"
)

// sanitizeName converts a name to be valid for Kubernetes resources
// - Replaces underscores with hyphens
// - Converts to lowercase
// - Removes invalid characters
func sanitizeName(name string) string {
	// Replace underscores with hyphens
	name = strings.ReplaceAll(name, "_", "-")
	// Convert to lowercase
	name = strings.ToLower(name)
	// Remove any characters that aren't alphanumeric, hyphens, or dots
	re := regexp.MustCompile(`[^a-z0-9\-.]`)
	name = re.ReplaceAllString(name, "")
	// Ensure it starts and ends with alphanumeric
	name = strings.Trim(name, "-.")
	return name
}

// Transformer converts a Compose project to Kubernetes manifests
type Transformer struct {
	project    *types.Project
	workingDir string
}

// NewTransformer creates a new transformer for the given project
func NewTransformer(project *types.Project) *Transformer {
	return &Transformer{
		project:    project,
		workingDir: project.WorkingDir,
	}
}

// ComposeSpec is the simplified compose spec for Jsonnet
type ComposeSpec struct {
	Name     string                    `json:"name"`
	Services map[string]ServiceSpec    `json:"services"`
	Volumes  map[string]VolumeSpec     `json:"volumes,omitempty"`
	Networks map[string]NetworkSpec    `json:"networks,omitempty"`
	Secrets  map[string]SecretSpec     `json:"secrets,omitempty"`
	Configs  map[string]ConfigSpec     `json:"configs,omitempty"`
}

// ServiceSpec represents a compose service
type ServiceSpec struct {
	Image       string            `json:"image,omitempty"`
	Build       *BuildSpec        `json:"build,omitempty"`
	Ports       []PortSpec        `json:"ports,omitempty"`
	Environment []EnvSpec         `json:"environment,omitempty"`
	Volumes     []VolumeMount     `json:"volumes,omitempty"`
	Networks    []string          `json:"networks,omitempty"`
	DependsOn   []string          `json:"depends_on,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Entrypoint  []string          `json:"entrypoint,omitempty"`
	Replicas    int               `json:"replicas,omitempty"`
	Secrets     []SecretRef       `json:"secrets,omitempty"`
	Configs     []ConfigRef       `json:"configs,omitempty"`
	HealthCheck *HealthCheckSpec  `json:"healthcheck,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Restart     string            `json:"restart,omitempty"`
}

type BuildSpec struct {
	Context    string `json:"context,omitempty"`
	Dockerfile string `json:"dockerfile,omitempty"`
}

type PortSpec struct {
	Target    uint32 `json:"target"`
	Published uint32 `json:"published"`
	Protocol  string `json:"protocol,omitempty"`
}

type EnvSpec struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type VolumeMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Type     string `json:"type,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type VolumeSpec struct {
	Driver string `json:"driver,omitempty"`
}

type NetworkSpec struct {
	External bool `json:"external,omitempty"`
}

type SecretSpec struct {
	File        string `json:"file,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type SecretRef struct {
	Source string `json:"source"`
	Target string `json:"target,omitempty"`
}

type ConfigSpec struct {
	File string `json:"file,omitempty"`
}

type ConfigRef struct {
	Source string `json:"source"`
	Target string `json:"target,omitempty"`
}

type HealthCheckSpec struct {
	Test        []string `json:"test,omitempty"`
	Interval    string   `json:"interval,omitempty"`
	Timeout     string   `json:"timeout,omitempty"`
	Retries     int      `json:"retries,omitempty"`
	StartPeriod string   `json:"start_period,omitempty"`
}

// ToSpec converts the compose project to a ComposeSpec
func (t *Transformer) ToSpec() *ComposeSpec {
	spec := &ComposeSpec{
		Name:     t.project.Name,
		Services: make(map[string]ServiceSpec),
		Volumes:  make(map[string]VolumeSpec),
		Networks: make(map[string]NetworkSpec),
		Secrets:  make(map[string]SecretSpec),
		Configs:  make(map[string]ConfigSpec),
	}

	// Convert services
	for _, svc := range t.project.Services {
		svcSpec := ServiceSpec{
			Image:    svc.Image,
			Replicas: 1,
			Labels:   svc.Labels,
			Restart:  svc.Restart,
		}

		// Build context
		if svc.Build != nil {
			svcSpec.Build = &BuildSpec{
				Context:    svc.Build.Context,
				Dockerfile: svc.Build.Dockerfile,
			}
			// If no image specified, generate one
			if svcSpec.Image == "" {
				svcSpec.Image = fmt.Sprintf("%s-%s:latest", t.project.Name, svc.Name)
			}
		}

		// Ports
		for _, p := range svc.Ports {
			published := p.Target // default to target if not specified
			if p.Published != "" {
				// Parse the published port string
				fmt.Sscanf(p.Published, "%d", &published)
			}
			port := PortSpec{
				Target:    p.Target,
				Published: published,
				Protocol:  p.Protocol,
			}
			if port.Protocol == "" {
				port.Protocol = "tcp"
			}
			svcSpec.Ports = append(svcSpec.Ports, port)
		}

		// Environment
		for k, v := range svc.Environment {
			val := ""
			if v != nil {
				val = *v
			}
			svcSpec.Environment = append(svcSpec.Environment, EnvSpec{Name: k, Value: val})
		}

		// Volumes
		for _, v := range svc.Volumes {
			vm := VolumeMount{
				Source:   v.Source,
				Target:   v.Target,
				Type:     v.Type,
				ReadOnly: v.ReadOnly,
			}
			svcSpec.Volumes = append(svcSpec.Volumes, vm)
		}

		// Networks
		for name := range svc.Networks {
			svcSpec.Networks = append(svcSpec.Networks, name)
		}

		// Dependencies
		for dep := range svc.DependsOn {
			svcSpec.DependsOn = append(svcSpec.DependsOn, dep)
		}

		// Command
		if len(svc.Command) > 0 {
			svcSpec.Command = svc.Command
		}

		// Entrypoint
		if len(svc.Entrypoint) > 0 {
			svcSpec.Entrypoint = svc.Entrypoint
		}

		// Replicas from deploy config
		if svc.Deploy != nil && svc.Deploy.Replicas != nil {
			svcSpec.Replicas = int(*svc.Deploy.Replicas)
		}

		// Secrets
		for _, s := range svc.Secrets {
			ref := SecretRef{Source: s.Source}
			if s.Target != "" {
				ref.Target = s.Target
			}
			svcSpec.Secrets = append(svcSpec.Secrets, ref)
		}

		// Configs
		for _, c := range svc.Configs {
			ref := ConfigRef{Source: c.Source}
			if c.Target != "" {
				ref.Target = c.Target
			}
			svcSpec.Configs = append(svcSpec.Configs, ref)
		}

		// Health check
		if svc.HealthCheck != nil && !svc.HealthCheck.Disable {
			hc := &HealthCheckSpec{
				Test: svc.HealthCheck.Test,
			}
			if svc.HealthCheck.Retries != nil {
				hc.Retries = int(*svc.HealthCheck.Retries)
			}
			if svc.HealthCheck.Interval != nil {
				hc.Interval = svc.HealthCheck.Interval.String()
			}
			if svc.HealthCheck.Timeout != nil {
				hc.Timeout = svc.HealthCheck.Timeout.String()
			}
			if svc.HealthCheck.StartPeriod != nil {
				hc.StartPeriod = svc.HealthCheck.StartPeriod.String()
			}
			svcSpec.HealthCheck = hc
		}

		spec.Services[svc.Name] = svcSpec
	}

	// Convert volumes
	for name, vol := range t.project.Volumes {
		spec.Volumes[name] = VolumeSpec{
			Driver: vol.Driver,
		}
	}

	// Convert networks
	for name, net := range t.project.Networks {
		// External is a types.External which has a boolean-like behavior
		spec.Networks[name] = NetworkSpec{
			External: bool(net.External),
		}
	}

	// Convert secrets
	for name, secret := range t.project.Secrets {
		spec.Secrets[name] = SecretSpec{
			File:        secret.File,
			Environment: secret.Environment,
		}
	}

	// Convert configs
	for name, cfg := range t.project.Configs {
		spec.Configs[name] = ConfigSpec{
			File: cfg.File,
		}
	}

	return spec
}

// Generate creates the workspace files
func (t *Transformer) Generate(ws *workspace.Workspace) error {
	spec := t.ToSpec()

	// Write spec.json
	specJSON, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal spec: %w", err)
	}
	if err := ws.WriteManifest("spec.json", specJSON); err != nil {
		return fmt.Errorf("failed to write spec.json: %w", err)
	}

	// Write kappal.libsonnet
	if err := ws.WriteLibsonnet("kappal.libsonnet", KappalLibsonnet); err != nil {
		return fmt.Errorf("failed to write kappal.libsonnet: %w", err)
	}

	// Write main.jsonnet
	mainJsonnet := `local kappal = import '../lib/kappal.libsonnet';
local spec = import '../manifests/spec.json';

kappal.project(spec)
`
	if err := ws.WriteMainJsonnet(mainJsonnet); err != nil {
		return fmt.Errorf("failed to write main.jsonnet: %w", err)
	}

	// Generate YAML manifests directly for kubectl apply
	if err := t.generateManifests(ws); err != nil {
		return fmt.Errorf("failed to generate manifests: %w", err)
	}

	return nil
}

// GenerateStandalone creates a standalone Tanka workspace
func (t *Transformer) GenerateStandalone(ws *workspace.Workspace) error {
	spec := t.ToSpec()

	// Write spec.json to environment
	specJSON, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal spec: %w", err)
	}
	if err := ws.WriteManifest("spec.json", specJSON); err != nil {
		return fmt.Errorf("failed to write spec.json: %w", err)
	}

	// Write kappal.libsonnet
	if err := ws.WriteLibsonnet("kappal.libsonnet", KappalLibsonnet); err != nil {
		return fmt.Errorf("failed to write kappal.libsonnet: %w", err)
	}

	// Write main.jsonnet with relative import
	mainJsonnet := `local kappal = import '../../lib/kappal.libsonnet';
local spec = import '../../manifests/spec.json';

kappal.project(spec)
`
	if err := ws.WriteMainJsonnet(mainJsonnet); err != nil {
		return fmt.Errorf("failed to write main.jsonnet: %w", err)
	}

	// Write Tanka spec
	if err := ws.WriteTankaSpec("https://127.0.0.1:6443", "default"); err != nil {
		return fmt.Errorf("failed to write tanka spec: %w", err)
	}

	// Write jsonnetfile.json
	if err := ws.WriteJsonnetfile(); err != nil {
		return fmt.Errorf("failed to write jsonnetfile.json: %w", err)
	}

	return nil
}

// generateManifests creates K8s YAML manifests directly
func (t *Transformer) generateManifests(ws *workspace.Workspace) error {
	spec := t.ToSpec()
	var manifests []string

	// Generate namespace
	ns := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    kappal.io/project: "%s"
`, spec.Name, spec.Name)
	manifests = append(manifests, ns)

	// Generate secrets
	for name, secret := range spec.Secrets {
		if secret.File != "" {
			k8sName := sanitizeName(name)
			// Read the secret file and base64 encode it
			secretPath := secret.File
			if !filepath.IsAbs(secretPath) {
				secretPath = filepath.Join(t.workingDir, secretPath)
			}
			secretData := ""
			if content, err := os.ReadFile(secretPath); err == nil {
				secretData = base64.StdEncoding.EncodeToString(content)
			}
			// Use original name as key (for mount subPath), sanitized name for resource name
			secretManifest := fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    kappal.io/project: "%s"
type: Opaque
data:
  %s: %s
`, k8sName, spec.Name, spec.Name, name, secretData)
			manifests = append(manifests, secretManifest)
		}
	}

	// Generate configmaps
	for name, cfg := range spec.Configs {
		k8sName := sanitizeName(name)
		// Read the config file
		configPath := cfg.File
		if !filepath.IsAbs(configPath) {
			configPath = filepath.Join(t.workingDir, configPath)
		}
		configData := ""
		if content, err := os.ReadFile(configPath); err == nil {
			// Escape for YAML multiline string
			configData = strings.ReplaceAll(string(content), "\n", "\n    ")
		}
		// Use original name as key (for mount subPath), sanitized name for resource name
		cmManifest := fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
  labels:
    kappal.io/project: "%s"
data:
  %s: |
    %s
`, k8sName, spec.Name, spec.Name, name, configData)
		manifests = append(manifests, cmManifest)
	}

	// Generate PVCs for named volumes
	for name := range spec.Volumes {
		pvcManifest := fmt.Sprintf(`---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
  labels:
    kappal.io/project: "%s"
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: local-path
`, name, spec.Name, spec.Name)
		manifests = append(manifests, pvcManifest)
	}

	// Generate NetworkPolicies for networks (for isolation)
	for name := range spec.Networks {
		if name == "default" {
			continue
		}
		npManifest := fmt.Sprintf(`---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s
  namespace: %s
  labels:
    kappal.io/project: "%s"
spec:
  podSelector:
    matchLabels:
      kappal.io/network: "%s"
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              kappal.io/network: "%s"
`, name, spec.Name, spec.Name, name, name)
		manifests = append(manifests, npManifest)
	}

	// Generate Deployments and Services for each service
	for name, svc := range spec.Services {
		manifests = append(manifests, t.generateDeployment(spec.Name, name, svc))
		if len(svc.Ports) > 0 {
			manifests = append(manifests, t.generateService(spec.Name, name, svc))
		}
	}

	// Write combined manifest
	combined := strings.Join(manifests, "\n---\n")
	return ws.WriteManifest("all.yaml", []byte(combined))
}

func (t *Transformer) generateDeployment(projectName, serviceName string, svc ServiceSpec) string {
	replicas := svc.Replicas
	if replicas < 1 {
		replicas = 1
	}

	// Build labels with optional network label
	labels := fmt.Sprintf(`        kappal.io/project: "%s"
        kappal.io/service: "%s"`, projectName, serviceName)
	if len(svc.Networks) > 0 {
		labels += fmt.Sprintf(`
        kappal.io/network: "%s"`, svc.Networks[0])
	}

	// Build container spec parts
	var containerParts []string

	// Ports
	if len(svc.Ports) > 0 {
		var portLines []string
		for _, p := range svc.Ports {
			protocol := strings.ToUpper(p.Protocol)
			if protocol == "" {
				protocol = "TCP"
			}
			portLines = append(portLines, fmt.Sprintf("        - containerPort: %d\n          protocol: %s", p.Target, protocol))
		}
		containerParts = append(containerParts, "        ports:\n"+strings.Join(portLines, "\n"))
	}

	// Environment
	if len(svc.Environment) > 0 {
		var envLines []string
		for _, e := range svc.Environment {
			envLines = append(envLines, fmt.Sprintf("        - name: \"%s\"\n          value: \"%s\"", e.Name, escapeYAML(e.Value)))
		}
		containerParts = append(containerParts, "        env:\n"+strings.Join(envLines, "\n"))
	}

	// Command
	if len(svc.Command) > 0 {
		var cmdLines []string
		for _, c := range svc.Command {
			cmdLines = append(cmdLines, fmt.Sprintf("        - \"%s\"", escapeYAML(c)))
		}
		containerParts = append(containerParts, "        command:\n"+strings.Join(cmdLines, "\n"))
	}

	// Volume mounts and volumes
	var volumeMountLines []string
	var volumeLines []string

	// Regular volumes
	for i, v := range svc.Volumes {
		volName := fmt.Sprintf("vol-%d", i)
		mountLine := fmt.Sprintf("        - name: %s\n          mountPath: \"%s\"", volName, v.Target)
		if v.ReadOnly {
			mountLine += "\n          readOnly: true"
		}
		volumeMountLines = append(volumeMountLines, mountLine)

		if v.Type == "volume" || v.Type == "" {
			volumeLines = append(volumeLines, fmt.Sprintf("      - name: %s\n        persistentVolumeClaim:\n          claimName: %s", volName, v.Source))
		} else if v.Type == "bind" {
			volumeLines = append(volumeLines, fmt.Sprintf("      - name: %s\n        hostPath:\n          path: \"%s\"", volName, v.Source))
		}
	}

	// Secret mounts
	for _, s := range svc.Secrets {
		target := s.Target
		if target == "" {
			target = s.Source
		}
		// Build mount path - if target already has /run/secrets/, use it directly
		mountPath := target
		if !strings.HasPrefix(target, "/run/secrets/") {
			mountPath = "/run/secrets/" + target
		}
		k8sSecretName := sanitizeName(s.Source)
		volName := fmt.Sprintf("secret-%s", k8sSecretName)
		// subPath must match the key in the secret data (original name)
		volumeMountLines = append(volumeMountLines, fmt.Sprintf("        - name: %s\n          mountPath: \"%s\"\n          subPath: %s\n          readOnly: true", volName, mountPath, s.Source))
		volumeLines = append(volumeLines, fmt.Sprintf("      - name: %s\n        secret:\n          secretName: %s", volName, k8sSecretName))
	}

	// Config mounts
	for _, c := range svc.Configs {
		target := c.Target
		if target == "" {
			target = "/" + c.Source
		}
		k8sConfigName := sanitizeName(c.Source)
		volName := fmt.Sprintf("config-%s", k8sConfigName)
		// subPath must match the key in the configmap data (original name)
		volumeMountLines = append(volumeMountLines, fmt.Sprintf("        - name: %s\n          mountPath: \"%s\"\n          subPath: %s\n          readOnly: true", volName, target, c.Source))
		volumeLines = append(volumeLines, fmt.Sprintf("      - name: %s\n        configMap:\n          name: %s", volName, k8sConfigName))
	}

	if len(volumeMountLines) > 0 {
		containerParts = append(containerParts, "        volumeMounts:\n"+strings.Join(volumeMountLines, "\n"))
	}

	containerSpec := strings.Join(containerParts, "\n")
	if containerSpec != "" {
		containerSpec = "\n" + containerSpec
	}

	volumeSpec := ""
	if len(volumeLines) > 0 {
		volumeSpec = "\n      volumes:\n" + strings.Join(volumeLines, "\n")
	}

	return fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    kappal.io/project: "%s"
    kappal.io/service: "%s"
spec:
  replicas: %d
  selector:
    matchLabels:
      kappal.io/project: "%s"
      kappal.io/service: "%s"
  template:
    metadata:
      labels:
%s
    spec:
      containers:
      - name: %s
        image: %s
        imagePullPolicy: IfNotPresent%s%s`, serviceName, projectName, projectName, serviceName, replicas,
		projectName, serviceName, labels,
		serviceName, svc.Image, containerSpec, volumeSpec)
}

func (t *Transformer) generateService(projectName, serviceName string, svc ServiceSpec) string {
	portItems := make([]string, 0, len(svc.Ports))
	for i, p := range svc.Ports {
		protocol := strings.ToUpper(p.Protocol)
		if protocol == "" {
			protocol = "TCP"
		}
		published := p.Published
		if published == 0 {
			published = p.Target
		}
		portItems = append(portItems, fmt.Sprintf(`  - name: port-%d
    port: %d
    targetPort: %d
    protocol: %s`, i, published, p.Target, protocol))
	}

	return fmt.Sprintf(`---
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  labels:
    kappal.io/project: "%s"
    kappal.io/service: "%s"
spec:
  type: LoadBalancer
  externalTrafficPolicy: Local
  selector:
    kappal.io/project: "%s"
    kappal.io/service: "%s"
  ports:
%s
`, serviceName, projectName, projectName, serviceName, projectName, serviceName, strings.Join(portItems, "\n"))
}

func escapeYAML(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
