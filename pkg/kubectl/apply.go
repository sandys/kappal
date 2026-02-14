package kubectl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/kappal-app/kappal/pkg/workspace"
)

// ApplyOpts configures the apply operation
type ApplyOpts struct {
	AutoApprove bool
	DryRun      bool
}

// DeleteOpts configures the delete operation
type DeleteOpts struct {
	AutoApprove   bool
	DeleteVolumes bool // If true, delete namespace (including PVCs). If false, only delete deployments/services.
}

// DiffOpts configures the diff operation
type DiffOpts struct {
}

// Apply applies the workspace manifests using kubectl
func Apply(ctx context.Context, ws *workspace.Workspace, kubeconfigPath string, opts ApplyOpts) error {
	manifestPath := filepath.Join(ws.GetManifestDir(), "all.yaml")

	// Check if manifest exists
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return fmt.Errorf("manifest not found: %s", manifestPath)
	}

	args := []string{
		"--kubeconfig", kubeconfigPath,
		"apply", "-f", manifestPath,
	}

	if opts.DryRun {
		args = append(args, "--dry-run=client")
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// Delete deletes resources in the namespace
// If DeleteVolumes is true, deletes the entire namespace (including PVCs)
// If DeleteVolumes is false, only deletes deployments and services (preserving PVCs)
func Delete(ctx context.Context, namespace, kubeconfigPath string, opts DeleteOpts) error {
	// Use a timeout to prevent kubectl from hanging on stuck finalizers
	deleteCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if opts.DeleteVolumes {
		// Delete entire namespace including PVCs
		args := []string{
			"--kubeconfig", kubeconfigPath,
			"delete", "namespace", namespace,
			"--ignore-not-found",
			"--wait=false",
		}
		cmd := exec.CommandContext(deleteCtx, "kubectl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Only delete deployments and services, preserve namespace and PVCs
	// This allows volumes to persist across down/up cycles
	args := []string{
		"--kubeconfig", kubeconfigPath,
		"delete", "deployments,services,configmaps,secrets,networkpolicies,jobs,roles,rolebindings",
		"-n", namespace,
		"--all",
		"--ignore-not-found",
		"--wait=false",
	}
	cmd := exec.CommandContext(deleteCtx, "kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Diff shows the diff between current and desired state
func Diff(ctx context.Context, ws *workspace.Workspace, kubeconfigPath string, opts DiffOpts) (string, error) {
	manifestPath := filepath.Join(ws.GetManifestDir(), "all.yaml")

	// Check if manifest exists
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return "", fmt.Errorf("manifest not found: %s", manifestPath)
	}

	args := []string{
		"--kubeconfig", kubeconfigPath,
		"diff", "-f", manifestPath,
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	output, err := cmd.CombinedOutput()

	// kubectl diff returns exit code 1 if there are differences
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return string(output), nil
		}
		return "", fmt.Errorf("diff failed: %w", err)
	}

	return string(output), nil
}

// Show renders the manifests without applying
func Show(ctx context.Context, ws *workspace.Workspace) ([]byte, error) {
	manifestPath := filepath.Join(ws.GetManifestDir(), "all.yaml")
	return os.ReadFile(manifestPath)
}
