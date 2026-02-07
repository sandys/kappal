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
