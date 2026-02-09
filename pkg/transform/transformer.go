package transform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

// DependsOnSpec represents a dependency with its condition
type DependsOnSpec struct {
	Service   string `json:"service"`
	Condition string `json:"condition,omitempty"`
}

// GetInitImage returns the image name for kappal-init containers.
// Defaults to "kappal-init:latest" — a local image built by LoadInitImage
// from the kappal-init binary and loaded into K3s containerd.
// Override with KAPPAL_INIT_IMAGE env var if a pre-built registry image is needed.
func GetInitImage() string {
	if img := os.Getenv("KAPPAL_INIT_IMAGE"); img != "" {
		return img
	}
	return "kappal-init:latest"
}

// ServiceSpec represents a compose service
type ServiceSpec struct {
	Image       string            `json:"image,omitempty"`
	Build       *BuildSpec        `json:"build,omitempty"`
	Ports       []PortSpec        `json:"ports,omitempty"`
	Environment []EnvSpec         `json:"environment,omitempty"`
	Volumes     []VolumeMount     `json:"volumes,omitempty"`
	Networks    []string          `json:"networks,omitempty"`
	DependsOn   []DependsOnSpec   `json:"depends_on,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Entrypoint  []string          `json:"entrypoint,omitempty"`
	Replicas    int               `json:"replicas,omitempty"`
	Secrets     []SecretRef       `json:"secrets,omitempty"`
	Configs     []ConfigRef       `json:"configs,omitempty"`
	HealthCheck *HealthCheckSpec  `json:"healthcheck,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Restart     string            `json:"restart,omitempty"`
	IsJob       bool              `json:"is_job,omitempty"`
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

	// First pass: collect image->built_image mappings for services with build contexts
	// This handles the pattern where multiple services share the same image but only one has a build
	imageToBuilt := make(map[string]string)
	for _, svc := range t.project.Services {
		if svc.Build != nil && svc.Image != "" {
			builtName := fmt.Sprintf("%s-%s:latest", t.project.Name, svc.Name)
			imageToBuilt[svc.Image] = builtName
		}
	}

	// Convert services
	for _, svc := range t.project.Services {
		// Skip services with profiles (not activated by default)
		if len(svc.Profiles) > 0 {
			continue
		}

		svcSpec := ServiceSpec{
			Image:    svc.Image,
			Replicas: 1,
			Labels:   svc.Labels,
			Restart:  svc.Restart,
			IsJob:    svc.Restart == "no",
		}

		// Build context
		if svc.Build != nil {
			svcSpec.Build = &BuildSpec{
				Context:    svc.Build.Context,
				Dockerfile: svc.Build.Dockerfile,
			}
			// Always use generated image name when building locally
			// The compose 'image:' field is for registry pulls, not local builds
			svcSpec.Image = fmt.Sprintf("%s-%s:latest", t.project.Name, svc.Name)
		} else if builtImage, ok := imageToBuilt[svc.Image]; ok {
			// Service uses an image that another service builds locally
			// Use the locally built image name
			svcSpec.Image = builtImage
		}

		// Ports
		for _, p := range svc.Ports {
			published := p.Target // default to target if not specified
			if p.Published != "" {
				// Parse the published port string; on failure, keep Target as fallback
				_, _ = fmt.Sscanf(p.Published, "%d", &published)
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

		// Dependencies with conditions
		for dep, config := range svc.DependsOn {
			condition := config.Condition
			if condition == "" {
				condition = "service_started"
			}
			svcSpec.DependsOn = append(svcSpec.DependsOn, DependsOnSpec{
				Service:   dep,
				Condition: condition,
			})
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
		k8sName := sanitizeName(name)
		pvcManifest := fmt.Sprintf(`---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
  labels:
    kappal.io/project: "%s"
    kappal.io/volume: "%s"
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: local-path
`, k8sName, spec.Name, spec.Name, name)
		manifests = append(manifests, pvcManifest)
	}

	// Generate NetworkPolicies for networks (for isolation)
	for name := range spec.Networks {
		if name == "default" {
			continue
		}
		k8sName := sanitizeName(name)
		npManifest := fmt.Sprintf(`---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %s
  namespace: %s
  labels:
    kappal.io/project: "%s"
    kappal.io/network: "%s"
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
`, k8sName, spec.Name, spec.Name, name, name, name)
		manifests = append(manifests, npManifest)
	}

	// Generate RBAC if any service has init container dependencies
	hasJobDependency := false
	hasServiceDependency := false
	for _, svc := range spec.Services {
		for _, dep := range svc.DependsOn {
			if dep.Condition == "service_completed_successfully" {
				hasJobDependency = true
			}
			if dep.Condition == "service_healthy" {
				hasServiceDependency = true
			}
		}
		if hasJobDependency && hasServiceDependency {
			break
		}
	}
	if hasJobDependency || hasServiceDependency {
		manifests = append(manifests, t.generateInitReaderRBAC(spec.Name, hasJobDependency, hasServiceDependency))
	}

	// Generate Deployments/Jobs and Services for each service
	// Services are created for ALL resources to enable DNS resolution,
	// not just those with published ports
	for name, svc := range spec.Services {
		if svc.IsJob {
			manifests = append(manifests, t.generateJob(spec.Name, name, svc, spec.Services))
		} else {
			manifests = append(manifests, t.generateDeployment(spec.Name, name, svc, spec.Services))
		}
		manifests = append(manifests, t.generateService(spec.Name, name, svc))
	}

	// Write combined manifest
	combined := strings.Join(manifests, "\n---\n")
	return ws.WriteManifest("all.yaml", []byte(combined))
}

// containerSpecParts holds the reusable parts of a container/pod spec
type containerSpecParts struct {
	containerSpec string
	volumeSpec    string
	labels        string
}

// buildContainerSpec extracts the shared container spec building logic
func (t *Transformer) buildContainerSpec(projectName, serviceName string, svc ServiceSpec) containerSpecParts {
	// Build labels with optional network label
	labels := fmt.Sprintf(`        kappal.io/project: "%s"
        kappal.io/service: "%s"`, projectName, serviceName)
	if len(svc.Networks) > 0 {
		labels += fmt.Sprintf(`
        kappal.io/network: "%s"`, svc.Networks[0])
	}

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

	// Entrypoint -> K8s command (replaces ENTRYPOINT)
	if len(svc.Entrypoint) > 0 {
		var cmdLines []string
		for _, c := range svc.Entrypoint {
			cmdLines = append(cmdLines, fmt.Sprintf("        - \"%s\"", escapeYAML(c)))
		}
		containerParts = append(containerParts, "        command:\n"+strings.Join(cmdLines, "\n"))
	}

	// Command -> K8s args (passed to entrypoint)
	if len(svc.Command) > 0 {
		var argLines []string
		for _, c := range svc.Command {
			argLines = append(argLines, fmt.Sprintf("        - \"%s\"", escapeYAML(c)))
		}
		containerParts = append(containerParts, "        args:\n"+strings.Join(argLines, "\n"))
	}

	// Volume mounts and volumes
	var volumeMountLines []string
	var volumeLines []string

	for i, v := range svc.Volumes {
		volName := fmt.Sprintf("vol-%d", i)
		mountLine := fmt.Sprintf("        - name: %s\n          mountPath: \"%s\"", volName, v.Target)
		if v.ReadOnly {
			mountLine += "\n          readOnly: true"
		}
		volumeMountLines = append(volumeMountLines, mountLine)

		switch v.Type {
		case "volume", "":
			pvcName := sanitizeName(v.Source)
			volumeLines = append(volumeLines, fmt.Sprintf("      - name: %s\n        persistentVolumeClaim:\n          claimName: %s", volName, pvcName))
		case "bind":
			volumeLines = append(volumeLines, fmt.Sprintf("      - name: %s\n        hostPath:\n          path: \"%s\"", volName, v.Source))
		}
	}

	// Secret mounts
	for _, s := range svc.Secrets {
		target := s.Target
		if target == "" {
			target = s.Source
		}
		mountPath := target
		if !strings.HasPrefix(target, "/run/secrets/") {
			mountPath = "/run/secrets/" + target
		}
		k8sSecretName := sanitizeName(s.Source)
		volName := fmt.Sprintf("secret-%s", k8sSecretName)
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
		volumeMountLines = append(volumeMountLines, fmt.Sprintf("        - name: %s\n          mountPath: \"%s\"\n          subPath: %s\n          readOnly: true", volName, target, c.Source))
		volumeLines = append(volumeLines, fmt.Sprintf("      - name: %s\n        configMap:\n          name: %s", volName, k8sConfigName))
	}

	if len(volumeMountLines) > 0 {
		containerParts = append(containerParts, "        volumeMounts:\n"+strings.Join(volumeMountLines, "\n"))
	}

	// Convert compose healthcheck to K8s readiness probe
	if svc.HealthCheck != nil && len(svc.HealthCheck.Test) > 0 {
		if probe := buildReadinessProbe(svc.HealthCheck); probe != "" {
			containerParts = append(containerParts, probe)
		}
	}

	containerSpec := strings.Join(containerParts, "\n")
	if containerSpec != "" {
		containerSpec = "\n" + containerSpec
	}

	volumeSpec := ""
	if len(volumeLines) > 0 {
		volumeSpec = "\n      volumes:\n" + strings.Join(volumeLines, "\n")
	}

	return containerSpecParts{
		containerSpec: containerSpec,
		volumeSpec:    volumeSpec,
		labels:        labels,
	}
}

// durationToSeconds parses a Go duration string and returns seconds (minimum 1).
func durationToSeconds(s string) int {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0
	}
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return secs
}

// buildReadinessProbe converts a compose HealthCheckSpec to K8s readinessProbe YAML.
func buildReadinessProbe(hc *HealthCheckSpec) string {
	if len(hc.Test) == 0 {
		return ""
	}

	// Parse test command: ["CMD-SHELL", "cmd"] or ["CMD", "arg1", ...] or ["NONE"]
	var command []string
	switch hc.Test[0] {
	case "CMD-SHELL":
		if len(hc.Test) < 2 {
			return ""
		}
		command = []string{"/bin/sh", "-c", hc.Test[1]}
	case "CMD":
		if len(hc.Test) < 2 {
			return ""
		}
		command = hc.Test[1:]
	case "NONE":
		return ""
	default:
		// Bare command (no prefix) — treat as shell command
		command = []string{"/bin/sh", "-c", strings.Join(hc.Test, " ")}
	}

	var cmdLines []string
	for _, c := range command {
		cmdLines = append(cmdLines, fmt.Sprintf("            - \"%s\"", escapeYAML(c)))
	}

	probe := "        readinessProbe:\n          exec:\n            command:\n" + strings.Join(cmdLines, "\n")

	if hc.Interval != "" {
		if secs := durationToSeconds(hc.Interval); secs > 0 {
			probe += fmt.Sprintf("\n          periodSeconds: %d", secs)
		}
	}
	if hc.Timeout != "" {
		if secs := durationToSeconds(hc.Timeout); secs > 0 {
			probe += fmt.Sprintf("\n          timeoutSeconds: %d", secs)
		}
	}
	if hc.Retries > 0 {
		probe += fmt.Sprintf("\n          failureThreshold: %d", hc.Retries)
	}
	if hc.StartPeriod != "" {
		if secs := durationToSeconds(hc.StartPeriod); secs > 0 {
			probe += fmt.Sprintf("\n          initialDelaySeconds: %d", secs)
		}
	}

	return probe
}

// buildInitContainerSpec generates the init container YAML for waiting on dependencies.
// It handles both service_completed_successfully (Jobs) and service_healthy (Deployments with healthchecks).
func (t *Transformer) buildInitContainerSpec(projectName string, svc ServiceSpec, allServices map[string]ServiceSpec) string {
	var waitForJobs []string
	var waitForServices []string
	for _, dep := range svc.DependsOn {
		switch dep.Condition {
		case "service_completed_successfully":
			if depSvc, ok := allServices[dep.Service]; ok && depSvc.IsJob {
				waitForJobs = append(waitForJobs, dep.Service)
			}
		case "service_healthy":
			if depSvc, ok := allServices[dep.Service]; ok && !depSvc.IsJob {
				waitForServices = append(waitForServices, dep.Service)
			}
		}
	}

	if len(waitForJobs) == 0 && len(waitForServices) == 0 {
		return ""
	}

	toJSONArray := func(items []string) string {
		if len(items) == 0 {
			return "[]"
		}
		parts := make([]string, len(items))
		for i, item := range items {
			parts[i] = fmt.Sprintf(`"%s"`, item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}

	specJSON := fmt.Sprintf(`{"namespace":"%s","waitForJobs":%s,"waitForServices":%s}`,
		projectName, toJSONArray(waitForJobs), toJSONArray(waitForServices))

	return fmt.Sprintf(`
      initContainers:
      - name: wait-for-deps
        image: %s
        imagePullPolicy: IfNotPresent
        command: ["kappal-init"]
        env:
        - name: KAPPAL_INIT_SPEC
          value: '%s'`, GetInitImage(), specJSON)
}

func (t *Transformer) generateDeployment(projectName, serviceName string, svc ServiceSpec, allServices map[string]ServiceSpec) string {
	replicas := svc.Replicas
	if replicas < 1 {
		replicas = 1
	}

	parts := t.buildContainerSpec(projectName, serviceName, svc)

	securityContextSpec := ""
	if len(svc.Volumes) > 0 {
		securityContextSpec = "\n      securityContext:\n        fsGroup: 999"
	}

	initContainerSpec := t.buildInitContainerSpec(projectName, svc, allServices)

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
    spec:%s%s
      containers:
      - name: %s
        image: %s
        imagePullPolicy: IfNotPresent%s%s`, serviceName, projectName, projectName, serviceName, replicas,
		projectName, serviceName, parts.labels, securityContextSpec, initContainerSpec,
		serviceName, svc.Image, parts.containerSpec, parts.volumeSpec)
}

func (t *Transformer) generateJob(projectName, serviceName string, svc ServiceSpec, allServices map[string]ServiceSpec) string {
	parts := t.buildContainerSpec(projectName, serviceName, svc)

	securityContextSpec := ""
	if len(svc.Volumes) > 0 {
		securityContextSpec = "\n      securityContext:\n        fsGroup: 999"
	}

	initContainerSpec := t.buildInitContainerSpec(projectName, svc, allServices)

	return fmt.Sprintf(`---
apiVersion: batch/v1
kind: Job
metadata:
  name: %s
  namespace: %s
  labels:
    kappal.io/project: "%s"
    kappal.io/service: "%s"
spec:
  backoffLimit: 3
  template:
    metadata:
      labels:
%s
    spec:
      restartPolicy: Never%s%s
      containers:
      - name: %s
        image: %s
        imagePullPolicy: IfNotPresent%s%s`, serviceName, projectName, projectName, serviceName,
		parts.labels, securityContextSpec, initContainerSpec,
		serviceName, svc.Image, parts.containerSpec, parts.volumeSpec)
}

func (t *Transformer) generateInitReaderRBAC(projectName string, needJobs, needPods bool) string {
	var rules []string
	if needJobs {
		rules = append(rules, `- apiGroups: ["batch"]
  resources: ["jobs"]
  verbs: ["get", "list", "watch"]`)
	}
	if needPods {
		rules = append(rules, `- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list"]`)
	}

	return fmt.Sprintf(`---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kappal-init-reader
  namespace: %s
  labels:
    kappal.io/project: "%s"
rules:
%s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kappal-init-reader
  namespace: %s
  labels:
    kappal.io/project: "%s"
subjects:
- kind: ServiceAccount
  name: default
  namespace: %s
roleRef:
  kind: Role
  name: kappal-init-reader
  apiGroup: rbac.authorization.k8s.io`, projectName, projectName, strings.Join(rules, "\n"), projectName, projectName, projectName)
}

// getDefaultPort returns the default port for well-known images
func getDefaultPort(image string) uint32 {
	imageLower := strings.ToLower(image)
	// Check for common database and service images
	switch {
	case strings.Contains(imageLower, "postgres"):
		return 5432
	case strings.Contains(imageLower, "mysql"), strings.Contains(imageLower, "mariadb"):
		return 3306
	case strings.Contains(imageLower, "redis"):
		return 6379
	case strings.Contains(imageLower, "mongo"):
		return 27017
	case strings.Contains(imageLower, "elasticsearch"):
		return 9200
	case strings.Contains(imageLower, "rabbitmq"):
		return 5672
	case strings.Contains(imageLower, "memcached"):
		return 11211
	case strings.Contains(imageLower, "nginx"):
		return 80
	case strings.Contains(imageLower, "httpd"), strings.Contains(imageLower, "apache"):
		return 80
	}
	return 0
}

func (t *Transformer) generateService(projectName, serviceName string, svc ServiceSpec) string {
	var portItems []string
	hasExternalPorts := len(svc.Ports) > 0

	if hasExternalPorts {
		// Use explicitly defined ports
		// K8s Service port and targetPort both use the container's target port.
		// The published (host) port is handled by Docker port bindings on the K3s container,
		// not by the K8s Service. Port chain: Host:published → K3s:target → ServiceLB:target → Pod:target
		for i, p := range svc.Ports {
			protocol := strings.ToUpper(p.Protocol)
			if protocol == "" {
				protocol = "TCP"
			}
			portItems = append(portItems, fmt.Sprintf(`  - name: port-%d
    port: %d
    targetPort: %d
    protocol: %s`, i, p.Target, p.Target, protocol))
		}
	} else {
		// No explicit ports - try to infer from image for internal service discovery
		defaultPort := getDefaultPort(svc.Image)
		if defaultPort > 0 {
			portItems = append(portItems, fmt.Sprintf(`  - name: port-0
    port: %d
    targetPort: %d
    protocol: TCP`, defaultPort, defaultPort))
		} else {
			// Can't determine port - use a placeholder port 80
			// This at least enables DNS resolution
			portItems = append(portItems, `  - name: port-0
    port: 80
    targetPort: 80
    protocol: TCP`)
		}
	}

	// Use LoadBalancer for services with external ports, ClusterIP for internal-only
	serviceType := "ClusterIP"
	externalTrafficPolicy := ""
	if hasExternalPorts {
		serviceType = "LoadBalancer"
		externalTrafficPolicy = "\n  externalTrafficPolicy: Local"
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
  type: %s%s
  selector:
    kappal.io/project: "%s"
    kappal.io/service: "%s"
  ports:
%s
`, serviceName, projectName, projectName, serviceName, serviceType, externalTrafficPolicy, projectName, serviceName, strings.Join(portItems, "\n"))
}

func escapeYAML(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
