package docker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadDockerignore(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{
			name:     "simple patterns",
			content:  "node_modules\n.git\n*.log",
			expected: []string{"node_modules", ".git", "*.log"},
		},
		{
			name:     "with comments",
			content:  "# Comment line\nnode_modules\n# Another comment\n.git",
			expected: []string{"node_modules", ".git"},
		},
		{
			name:     "with empty lines",
			content:  "node_modules\n\n.git\n\n*.log\n",
			expected: []string{"node_modules", ".git", "*.log"},
		},
		{
			name:     "with whitespace",
			content:  "  node_modules  \n\t.git\t\n  # comment  ",
			expected: []string{"node_modules", ".git"},
		},
		{
			name:     "empty file",
			content:  "",
			expected: nil,
		},
		{
			name:     "only comments",
			content:  "# just a comment\n# another one",
			expected: nil,
		},
		{
			name:     "negation patterns",
			content:  "*.log\n!important.log",
			expected: []string{"*.log", "!important.log"},
		},
		{
			name:     "directory patterns",
			content:  "build/\ntmp/\n**/*.tmp",
			expected: []string{"build/", "tmp/", "**/*.tmp"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory
			tmpDir, err := os.MkdirTemp("", "dockerignore-test")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Write .dockerignore file
			dockerignorePath := filepath.Join(tmpDir, ".dockerignore")
			if err := os.WriteFile(dockerignorePath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write .dockerignore: %v", err)
			}

			// Test readDockerignore
			result, err := readDockerignore(tmpDir)
			if err != nil {
				t.Fatalf("readDockerignore failed: %v", err)
			}

			// Compare results
			if len(result) != len(tt.expected) {
				t.Errorf("readDockerignore() returned %d patterns, want %d\nGot: %v\nWant: %v",
					len(result), len(tt.expected), result, tt.expected)
				return
			}

			for i, pattern := range result {
				if pattern != tt.expected[i] {
					t.Errorf("pattern[%d] = %q, want %q", i, pattern, tt.expected[i])
				}
			}
		})
	}
}

func TestReadDockerignoreNoFile(t *testing.T) {
	// Create temp directory without .dockerignore
	tmpDir, err := os.MkdirTemp("", "dockerignore-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Should return nil, nil when no .dockerignore exists
	result, err := readDockerignore(tmpDir)
	if err != nil {
		t.Errorf("readDockerignore() should not error for missing file, got: %v", err)
	}
	if result != nil {
		t.Errorf("readDockerignore() should return nil for missing file, got: %v", result)
	}
}

func TestReadDockerignorePermissionError(t *testing.T) {
	// Skip this test on systems where we can't change permissions (e.g., some CI environments)
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "dockerignore-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		// Restore permissions before cleanup
		_ = os.Chmod(filepath.Join(tmpDir, ".dockerignore"), 0644)
		_ = os.RemoveAll(tmpDir)
	}()

	// Write .dockerignore file with restricted permissions
	dockerignorePath := filepath.Join(tmpDir, ".dockerignore")
	if err := os.WriteFile(dockerignorePath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write .dockerignore: %v", err)
	}

	// Make file unreadable
	if err := os.Chmod(dockerignorePath, 0000); err != nil {
		t.Fatalf("failed to change permissions: %v", err)
	}

	// Should return an error for permission denied
	_, err = readDockerignore(tmpDir)
	if err == nil {
		t.Error("readDockerignore() should error for unreadable file")
	}
}
