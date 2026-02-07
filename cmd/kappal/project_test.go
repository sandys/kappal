package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestSanitizeDNS1123Label(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"MyApp", "myapp"},
		{"my_app", "my-app"},
		{"my.app", "my-app"},
		{"--leading--", "leading"},
		{"trailing--", "trailing"},
		{"UPPER_CASE.Dots", "upper-case-dots"},
		{"a", "a"},
		{"", ""},
		{"---", ""},
		{"hello world!", "hello-world"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeDNS1123Label(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeDNS1123Label(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeDNS1123LabelTruncation(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789"
	got := sanitizeDNS1123Label(long)
	if len(got) > len(long) {
		t.Errorf("sanitizeDNS1123Label should not expand input; got len %d", len(got))
	}
}

func TestDirHashDeterminism(t *testing.T) {
	h1 := dirHash("/home/user/myapp")
	h2 := dirHash("/home/user/myapp")
	if h1 != h2 {
		t.Errorf("dirHash not deterministic: %q != %q", h1, h2)
	}
}

func TestDirHashUniqueness(t *testing.T) {
	h1 := dirHash("/home/user/myapp")
	h2 := dirHash("/home/user/worktrees/myapp")
	if h1 == h2 {
		t.Errorf("dirHash collision for different paths: both %q", h1)
	}
}

func TestDirHashLength(t *testing.T) {
	h := dirHash("/some/path")
	if len(h) != 8 {
		t.Errorf("dirHash length = %d, want 8", len(h))
	}
}

func TestResolveProjectNameExplicit(t *testing.T) {
	got := resolveProjectName("myname", "/any/dir")
	if got != "myname" {
		t.Errorf("resolveProjectName with explicit name = %q, want %q", got, "myname")
	}
}

func TestBuildProjectNameFormat(t *testing.T) {
	got := buildProjectName("/home/user/myapp")
	pattern := regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-[0-9a-f]{8}$`)
	if !pattern.MatchString(got) {
		t.Errorf("buildProjectName(%q) = %q, does not match <base>-<8hexchars>", "/home/user/myapp", got)
	}
}

func TestBuildProjectNameDifferentPaths(t *testing.T) {
	a := buildProjectName("/home/user/myapp")
	b := buildProjectName("/home/user/worktrees/myapp")
	if a == b {
		t.Errorf("same basename, different dirs should produce different names; both %q", a)
	}
}

func TestBuildProjectNameSymlinkResilience(t *testing.T) {
	// Create a real directory and a symlink pointing to it.
	realDir := t.TempDir()
	parentDir := t.TempDir()
	symlink := filepath.Join(parentDir, "link")
	if err := os.Symlink(realDir, symlink); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	fromReal := buildProjectName(realDir)
	fromLink := buildProjectName(symlink)
	if fromReal != fromLink {
		t.Errorf("symlink divergence: real=%q symlink=%q", fromReal, fromLink)
	}
}
