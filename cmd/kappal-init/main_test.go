package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareWritablePathsCreatesDir(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "new-dir")

	if err := prepareWritablePaths([]string{target}); err != nil {
		t.Fatalf("prepareWritablePaths failed: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got %s", info.Mode().String())
	}
	if mode := info.Mode().Perm(); mode != 0777 {
		t.Fatalf("expected mode 0777, got %04o", mode)
	}
}

func TestPrepareWritablePathsChmodsExistingDir(t *testing.T) {
	target := t.TempDir()
	if err := os.Chmod(target, 0755); err != nil {
		t.Fatalf("chmod setup: %v", err)
	}

	if err := prepareWritablePaths([]string{target}); err != nil {
		t.Fatalf("prepareWritablePaths failed: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0777 {
		t.Fatalf("expected mode 0777, got %04o", mode)
	}
}

func TestPrepareWritablePathsChmodsFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "data.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := prepareWritablePaths([]string{target}); err != nil {
		t.Fatalf("prepareWritablePaths failed: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0666 {
		t.Fatalf("expected mode 0666, got %04o", mode)
	}
}

func TestPrepareWritablePathsRejectsRoot(t *testing.T) {
	err := prepareWritablePaths([]string{"/"})
	if err == nil {
		t.Fatal("expected error for root path, got nil")
	}
}

func TestPrepareWritablePathsRejectsRelative(t *testing.T) {
	err := prepareWritablePaths([]string{"relative/path"})
	if err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
}
