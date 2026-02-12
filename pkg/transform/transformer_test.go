package transform

import (
	"strings"
	"testing"
	"unicode"
)

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"my_secret", "my-secret"},
		{"app_config", "app-config"},
		{"MyService", "myservice"},
		{"simple", "simple"},
		{"with-dash", "with-dash"},
		{"with.dot", "with.dot"},
		{"UPPERCASE", "uppercase"},
		{"with_multiple_underscores", "with-multiple-underscores"},
		{"123numeric", "123numeric"},
		{"-leading-dash", "leading-dash"},
		{"trailing-dash-", "trailing-dash"},
		{"special@chars!", "specialchars"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := sanitizeName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizeNameK8sCompliant(t *testing.T) {
	// Test that sanitized names are valid K8s resource names
	// Must match: [a-z0-9]([-a-z0-9]*[a-z0-9])?
	testCases := []string{
		"my_secret",
		"app_config",
		"Test_Service",
		"LOUD_NAME",
		"123_456",
	}

	for _, tc := range testCases {
		result := sanitizeName(tc)

		// Check lowercase
		if result != strings.ToLower(result) {
			t.Errorf("sanitizeName(%q) = %q is not lowercase", tc, result)
		}

		// Check no underscores
		if strings.Contains(result, "_") {
			t.Errorf("sanitizeName(%q) = %q contains underscore", tc, result)
		}

		// Check starts/ends with alphanumeric
		if len(result) > 0 {
			first, last := rune(result[0]), rune(result[len(result)-1])
			if !unicode.IsLower(first) && !unicode.IsDigit(first) {
				t.Errorf("sanitizeName(%q) = %q doesn't start with alphanumeric", tc, result)
			}
			if !unicode.IsLower(last) && !unicode.IsDigit(last) {
				t.Errorf("sanitizeName(%q) = %q doesn't end with alphanumeric", tc, result)
			}
		}
	}
}

func TestSecretMountPathNotDuplicated(t *testing.T) {
	// Test cases where target already has /run/secrets/ prefix
	testCases := []struct {
		target   string
		expected string
	}{
		{"/run/secrets/my_secret", "/run/secrets/my_secret"},
		{"my_secret", "/run/secrets/my_secret"},
		{"/run/secrets/nested/path", "/run/secrets/nested/path"},
	}

	for _, tc := range testCases {
		mountPath := tc.target
		if !strings.HasPrefix(tc.target, "/run/secrets/") {
			mountPath = "/run/secrets/" + tc.target
		}

		if mountPath != tc.expected {
			t.Errorf("mount path for target %q = %q, want %q", tc.target, mountPath, tc.expected)
		}

		// Ensure no double prefix
		if strings.Contains(mountPath, "/run/secrets//run/secrets/") {
			t.Errorf("mount path %q has duplicated prefix", mountPath)
		}
	}
}

func TestCommandVsArgsMapping(t *testing.T) {
	// Test that compose command maps to K8s args (not command)
	// and compose entrypoint maps to K8s command
	// This is critical for containers like postgres that rely on their entrypoint

	t.Run("command_only_generates_args", func(t *testing.T) {
		svc := ServiceSpec{
			Image:   "postgres:16",
			Command: []string{"postgres", "-c", "shared_preload_libraries=pg_cron"},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		deployment := transformer.generateDeployment("test", "postgres", svc, nil)

		// Should have args, not command (to preserve entrypoint)
		if strings.Contains(deployment, "        command:") {
			t.Error("compose command should generate K8s args, not command (to preserve ENTRYPOINT)")
		}
		if !strings.Contains(deployment, "        args:") {
			t.Error("compose command should generate K8s args")
		}
		if !strings.Contains(deployment, `"postgres"`) {
			t.Error("args should contain the command values")
		}
	})

	t.Run("entrypoint_generates_command", func(t *testing.T) {
		svc := ServiceSpec{
			Image:      "myapp:latest",
			Entrypoint: []string{"/custom-entrypoint.sh"},
			Command:    []string{"--flag", "value"},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		deployment := transformer.generateDeployment("test", "myapp", svc, nil)

		// Should have both command (from entrypoint) and args (from command)
		if !strings.Contains(deployment, "        command:") {
			t.Error("compose entrypoint should generate K8s command")
		}
		if !strings.Contains(deployment, `"/custom-entrypoint.sh"`) {
			t.Error("command should contain entrypoint values")
		}
		if !strings.Contains(deployment, "        args:") {
			t.Error("compose command should generate K8s args")
		}
	})

	t.Run("neither_command_nor_entrypoint", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "nginx:latest",
		}

		transformer := &Transformer{workingDir: "/tmp"}
		deployment := transformer.generateDeployment("test", "nginx", svc, nil)

		// Should have neither command nor args
		if strings.Contains(deployment, "        command:") {
			t.Error("should not have command when entrypoint not specified")
		}
		if strings.Contains(deployment, "        args:") {
			t.Error("should not have args when command not specified")
		}
	})
}

func TestReadinessProbeFromHealthcheck(t *testing.T) {
	t.Run("CMD-SHELL healthcheck generates exec readiness probe", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "postgres:16",
			HealthCheck: &HealthCheckSpec{
				Test:        []string{"CMD-SHELL", "pg_isready -U postgres"},
				Interval:    "10s",
				Timeout:     "5s",
				Retries:     3,
				StartPeriod: "30s",
			},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		deployment := transformer.generateDeployment("test", "postgres", svc, nil)

		if !strings.Contains(deployment, "readinessProbe:") {
			t.Fatal("deployment should contain readinessProbe")
		}
		if !strings.Contains(deployment, "exec:") {
			t.Error("readiness probe should use exec")
		}
		if !strings.Contains(deployment, `"/bin/sh"`) {
			t.Error("CMD-SHELL should use /bin/sh -c")
		}
		if !strings.Contains(deployment, `"pg_isready -U postgres"`) {
			t.Error("probe should contain healthcheck command")
		}
		if !strings.Contains(deployment, "periodSeconds: 10") {
			t.Error("interval should map to periodSeconds")
		}
		if !strings.Contains(deployment, "timeoutSeconds: 5") {
			t.Error("timeout should map to timeoutSeconds")
		}
		if !strings.Contains(deployment, "failureThreshold: 3") {
			t.Error("retries should map to failureThreshold")
		}
		if !strings.Contains(deployment, "initialDelaySeconds: 30") {
			t.Error("start_period should map to initialDelaySeconds")
		}
	})

	t.Run("CMD healthcheck generates exec probe with args", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "redis:7",
			HealthCheck: &HealthCheckSpec{
				Test:    []string{"CMD", "redis-cli", "ping"},
				Retries: 5,
			},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		deployment := transformer.generateDeployment("test", "redis", svc, nil)

		if !strings.Contains(deployment, "readinessProbe:") {
			t.Fatal("deployment should contain readinessProbe")
		}
		if !strings.Contains(deployment, `"redis-cli"`) {
			t.Error("CMD probe should use command args directly")
		}
		if !strings.Contains(deployment, `"ping"`) {
			t.Error("CMD probe should include all args")
		}
		// CMD should NOT wrap in /bin/sh -c
		if strings.Contains(deployment, `"/bin/sh"`) {
			t.Error("CMD probe should not use /bin/sh")
		}
		if !strings.Contains(deployment, "failureThreshold: 5") {
			t.Error("retries should map to failureThreshold")
		}
	})

	t.Run("NONE healthcheck generates no probe", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "myapp:latest",
			HealthCheck: &HealthCheckSpec{
				Test: []string{"NONE"},
			},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		deployment := transformer.generateDeployment("test", "myapp", svc, nil)

		if strings.Contains(deployment, "readinessProbe:") {
			t.Error("NONE healthcheck should not produce a readiness probe")
		}
	})

	t.Run("no healthcheck generates no probe", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "nginx:latest",
		}

		transformer := &Transformer{workingDir: "/tmp"}
		deployment := transformer.generateDeployment("test", "nginx", svc, nil)

		if strings.Contains(deployment, "readinessProbe:") {
			t.Error("no healthcheck should not produce a readiness probe")
		}
	})
}

func TestInitContainerServiceHealthy(t *testing.T) {
	allServices := map[string]ServiceSpec{
		"postgres": {Image: "postgres:16", IsJob: false},
		"redis":    {Image: "redis:7", IsJob: false},
		"migrate":  {Image: "migrate:latest", IsJob: true},
	}

	t.Run("service_healthy generates init with waitForServices", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "app:latest",
			DependsOn: []DependsOnSpec{
				{Service: "postgres", Condition: "service_healthy"},
			},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		initSpec := transformer.buildInitContainerSpec("test", svc, allServices)

		if initSpec == "" {
			t.Fatal("service_healthy dep should generate init container")
		}
		if !strings.Contains(initSpec, "wait-for-deps") {
			t.Error("init container should be named wait-for-deps")
		}
		if !strings.Contains(initSpec, `"waitForServices":["postgres"]`) {
			t.Error("init spec should include waitForServices with postgres")
		}
		if !strings.Contains(initSpec, `"waitForJobs":[]`) {
			t.Error("init spec should include empty waitForJobs")
		}
	})

	t.Run("service_completed_successfully generates init with waitForJobs", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "app:latest",
			DependsOn: []DependsOnSpec{
				{Service: "migrate", Condition: "service_completed_successfully"},
			},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		initSpec := transformer.buildInitContainerSpec("test", svc, allServices)

		if initSpec == "" {
			t.Fatal("service_completed_successfully dep should generate init container")
		}
		if !strings.Contains(initSpec, `"waitForJobs":["migrate"]`) {
			t.Error("init spec should include waitForJobs with migrate")
		}
		if !strings.Contains(initSpec, `"waitForServices":[]`) {
			t.Error("init spec should include empty waitForServices")
		}
	})

	t.Run("both conditions combined", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "app:latest",
			DependsOn: []DependsOnSpec{
				{Service: "postgres", Condition: "service_healthy"},
				{Service: "migrate", Condition: "service_completed_successfully"},
			},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		initSpec := transformer.buildInitContainerSpec("test", svc, allServices)

		if initSpec == "" {
			t.Fatal("combined deps should generate init container")
		}
		if !strings.Contains(initSpec, `"waitForJobs":["migrate"]`) {
			t.Error("init spec should include waitForJobs")
		}
		if !strings.Contains(initSpec, `"waitForServices":["postgres"]`) {
			t.Error("init spec should include waitForServices")
		}
	})

	t.Run("service_healthy on a Job is ignored", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "app:latest",
			DependsOn: []DependsOnSpec{
				{Service: "migrate", Condition: "service_healthy"},
			},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		initSpec := transformer.buildInitContainerSpec("test", svc, allServices)

		if initSpec != "" {
			t.Error("service_healthy on a Job should not generate init container")
		}
	})

	t.Run("service_started generates no init container", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "app:latest",
			DependsOn: []DependsOnSpec{
				{Service: "postgres", Condition: "service_started"},
			},
		}

		transformer := &Transformer{workingDir: "/tmp"}
		initSpec := transformer.buildInitContainerSpec("test", svc, allServices)

		if initSpec != "" {
			t.Error("service_started should not generate init container")
		}
	})
}

func TestInitContainerWritableBindMounts(t *testing.T) {
	transformer := &Transformer{workingDir: "/tmp"}

	t.Run("writable bind mount generates init container", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "app:latest",
			Volumes: []VolumeMount{
				{Source: "/host/data", Target: "/data", Type: "bind"},
			},
		}

		initSpec := transformer.buildInitContainerSpec("test", svc, nil)
		if initSpec == "" {
			t.Fatal("writable bind mount should generate init container")
		}
		if !strings.Contains(initSpec, `"prepareWritablePaths":["/data"]`) {
			t.Error("init spec should include prepareWritablePaths with /data")
		}
		if !strings.Contains(initSpec, "runAsUser: 0") {
			t.Error("writable bind mount init should run as root")
		}
		if !strings.Contains(initSpec, "volumeMounts:") {
			t.Error("init spec should include volume mounts")
		}
		if !strings.Contains(initSpec, "mountPath: \"/data\"") {
			t.Error("init spec should mount the bind target path")
		}
	})

	t.Run("read-only bind mount does not generate init when no dependencies", func(t *testing.T) {
		svc := ServiceSpec{
			Image: "app:latest",
			Volumes: []VolumeMount{
				{Source: "/host/data", Target: "/data", Type: "bind", ReadOnly: true},
			},
		}

		initSpec := transformer.buildInitContainerSpec("test", svc, nil)
		if initSpec != "" {
			t.Error("read-only bind mount should not generate init container by itself")
		}
	})

	t.Run("dependencies and writable paths are both included", func(t *testing.T) {
		allServices := map[string]ServiceSpec{
			"migrate": {Image: "migrate:latest", IsJob: true},
		}
		svc := ServiceSpec{
			Image: "app:latest",
			Volumes: []VolumeMount{
				{Source: "/host/data", Target: "/data", Type: "bind"},
			},
			DependsOn: []DependsOnSpec{
				{Service: "migrate", Condition: "service_completed_successfully"},
			},
		}

		initSpec := transformer.buildInitContainerSpec("test", svc, allServices)
		if initSpec == "" {
			t.Fatal("combined dependency + writable bind should generate init container")
		}
		if !strings.Contains(initSpec, `"waitForJobs":["migrate"]`) {
			t.Error("init spec should include waitForJobs")
		}
		if !strings.Contains(initSpec, `"prepareWritablePaths":["/data"]`) {
			t.Error("init spec should include prepareWritablePaths")
		}
	})
}

func TestRBACGenerationForDependencies(t *testing.T) {
	transformer := &Transformer{workingDir: "/tmp"}

	t.Run("job dependency generates batch/jobs RBAC", func(t *testing.T) {
		rbac := transformer.generateInitReaderRBAC("test", true, false)

		if !strings.Contains(rbac, `apiGroups: ["batch"]`) {
			t.Error("should include batch apiGroup for jobs")
		}
		if !strings.Contains(rbac, `resources: ["jobs"]`) {
			t.Error("should include jobs resource")
		}
		if strings.Contains(rbac, `resources: ["pods"]`) {
			t.Error("should not include pods when only job deps")
		}
		if !strings.Contains(rbac, "kappal-init-reader") {
			t.Error("role should be named kappal-init-reader")
		}
	})

	t.Run("service_healthy dependency generates pods RBAC", func(t *testing.T) {
		rbac := transformer.generateInitReaderRBAC("test", false, true)

		if !strings.Contains(rbac, `resources: ["pods"]`) {
			t.Error("should include pods resource")
		}
		if !strings.Contains(rbac, `apiGroups: [""]`) {
			t.Error("should include core apiGroup for pods")
		}
		if strings.Contains(rbac, `resources: ["jobs"]`) {
			t.Error("should not include jobs when only service deps")
		}
	})

	t.Run("both dependencies generate combined RBAC", func(t *testing.T) {
		rbac := transformer.generateInitReaderRBAC("test", true, true)

		if !strings.Contains(rbac, `resources: ["jobs"]`) {
			t.Error("should include jobs resource")
		}
		if !strings.Contains(rbac, `resources: ["pods"]`) {
			t.Error("should include pods resource")
		}
	})
}

func TestDurationToSeconds(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"10s", 10},
		{"5s", 5},
		{"1m", 60},
		{"1m30s", 90},
		{"500ms", 1},   // rounds up to minimum 1
		{"0s", 0},      // zero returns 0
		{"invalid", 0}, // invalid returns 0
		{"", 0},        // empty returns 0
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := durationToSeconds(tt.input)
			if result != tt.expected {
				t.Errorf("durationToSeconds(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGenerateManifestsServiceHealthyEndToEnd(t *testing.T) {
	// End-to-end: a compose project with service_healthy dependency should produce
	// correct readiness probe, init container, and RBAC in the combined manifests.
	allServices := map[string]ServiceSpec{
		"postgres": {
			Image: "postgres:16",
			HealthCheck: &HealthCheckSpec{
				Test:     []string{"CMD-SHELL", "pg_isready -U postgres"},
				Interval: "10s",
				Timeout:  "5s",
				Retries:  3,
			},
		},
		"app": {
			Image: "myapp:latest",
			DependsOn: []DependsOnSpec{
				{Service: "postgres", Condition: "service_healthy"},
			},
		},
	}

	transformer := &Transformer{workingDir: "/tmp"}

	// Check postgres deployment has readiness probe
	pgDeployment := transformer.generateDeployment("test", "postgres", allServices["postgres"], allServices)
	if !strings.Contains(pgDeployment, "readinessProbe:") {
		t.Error("postgres deployment should have readinessProbe from healthcheck")
	}
	if !strings.Contains(pgDeployment, "pg_isready") {
		t.Error("readiness probe should contain the healthcheck command")
	}

	// Check app deployment has init container waiting for postgres
	appDeployment := transformer.generateDeployment("test", "app", allServices["app"], allServices)
	if !strings.Contains(appDeployment, "initContainers:") {
		t.Error("app deployment should have init containers")
	}
	if !strings.Contains(appDeployment, "wait-for-deps") {
		t.Error("init container should be named wait-for-deps")
	}
	if !strings.Contains(appDeployment, "waitForServices") {
		t.Error("init container spec should reference waitForServices")
	}
	if !strings.Contains(appDeployment, "postgres") {
		t.Error("init container should wait for postgres")
	}

	// App should NOT have a readiness probe (no healthcheck defined)
	if strings.Contains(appDeployment, "readinessProbe:") {
		t.Error("app deployment should not have readinessProbe (no healthcheck)")
	}
}

func TestServicePortUsesTargetNotPublished(t *testing.T) {
	// When published port differs from target port, the K8s Service should use
	// the target (container) port for both port and targetPort.
	// The published (host) port is handled by Docker port bindings on the K3s container.
	svc := ServiceSpec{
		Image: "nginx:latest",
		Ports: []PortSpec{{Target: 8080, Published: 8082, Protocol: "tcp"}},
	}

	transformer := &Transformer{workingDir: "/tmp"}
	service := transformer.generateService("test", "web", svc)

	// Should contain port: 8080 (target), NOT port: 8082 (published)
	if !strings.Contains(service, "port: 8080") {
		t.Error("K8s Service port should use target port (8080), not published port")
	}
	if strings.Contains(service, "port: 8082") {
		t.Error("K8s Service port should NOT use published port (8082)")
	}
	if !strings.Contains(service, "targetPort: 8080") {
		t.Error("K8s Service targetPort should use target port (8080)")
	}
}
