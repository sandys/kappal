package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nonDNSChars = regexp.MustCompile(`[^a-z0-9-]`)

// sanitizeDNS1123Label lowercases the input, replaces characters outside
// [a-z0-9-] with "-", and trims leading/trailing hyphens.
func sanitizeDNS1123Label(s string) string {
	s = strings.ToLower(s)
	s = nonDNSChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// dirHash returns the first 8 hex characters of the SHA-256 of absDir.
func dirHash(absDir string) string {
	h := sha256.Sum256([]byte(absDir))
	return fmt.Sprintf("%x", h[:4])
}

// buildProjectName computes "<sanitised-base>-<8-char-hash>" from the compose
// directory path. It resolves symlinks so that the same physical directory always
// produces the same project name regardless of the path used.
//
// When KAPPAL_HOST_DIR is set (Docker wrapper mode), it is used as the hash source
// instead of the container-side path. This ensures different host directories produce
// different project names even when they all map to /project inside the container.
func buildProjectName(composeDir string) string {
	hostDir := os.Getenv("KAPPAL_HOST_DIR")
	if hostDir != "" {
		base := sanitizeDNS1123Label(filepath.Base(composeDir))
		if len(base) > 54 {
			base = base[:54]
		}
		if base == "" {
			base = "default"
		}
		return base + "-" + dirHash(hostDir+":"+composeDir)
	}

	absDir, err := filepath.EvalSymlinks(composeDir)
	if err != nil {
		// EvalSymlinks can fail if the directory doesn't exist yet (e.g. during clean).
		// Fall back to Abs which doesn't require the path to exist.
		absDir, err = filepath.Abs(composeDir)
		if err != nil {
			absDir = composeDir
		}
	}

	base := sanitizeDNS1123Label(filepath.Base(absDir))
	if len(base) > 54 {
		base = base[:54]
	}
	if base == "" {
		base = "default"
	}

	return base + "-" + dirHash(absDir)
}

// resolveProjectName determines the project name:
//  1. If the user supplied -p, return that unchanged.
//  2. Otherwise, build "<sanitised-base>-<8-char-hash>" from the compose directory.
func resolveProjectName(userProjectName string, composeDir string) string {
	if userProjectName != "" {
		return userProjectName
	}
	return buildProjectName(composeDir)
}
