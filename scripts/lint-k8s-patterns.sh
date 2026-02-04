#!/bin/bash
# Lint for correct Kubernetes patterns based on lessons learned
#
# Key patterns:
# 1. Compose `command` → K8s `args` (NOT K8s `command`)
# 2. Compose `entrypoint` → K8s `command`
# 3. Every Deployment should have a Service for DNS resolution
# 4. K3s should use named Docker volumes (not bind mounts) for persistence
#
set -e

echo "Checking for correct Kubernetes patterns..."

ERRORS=0
WARNINGS=0

# =============================================================================
# Check 1: Compose command/entrypoint mapping
# =============================================================================
# In Docker:   ENTRYPOINT + CMD
# In K8s:      command + args
# In Compose:  entrypoint + command
#
# Correct mapping:
#   Compose entrypoint → K8s command (replaces ENTRYPOINT)
#   Compose command    → K8s args (replaces CMD)
#
# WRONG pattern in transformer.go:
#   if len(svc.Command) > 0 { ... containerParts = append(..., "command:\n"...) }
#
# CORRECT pattern in transformer.go:
#   if len(svc.Command) > 0 { ... containerParts = append(..., "args:\n"...) }
#   if len(svc.Entrypoint) > 0 { ... containerParts = append(..., "command:\n"...) }

check_command_args_mapping() {
    local transformer="pkg/transform/transformer.go"
    if [ ! -f "$transformer" ]; then
        echo "WARNING: transformer.go not found, skipping command/args check"
        return 0
    fi

    # The correct pattern in generateDeployment:
    #   Command -> K8s args (replaces CMD, passed to entrypoint)
    #   Entrypoint -> K8s command (replaces ENTRYPOINT)
    #
    # Look for evidence of correct mapping in manifest generation (not spec structs)

    # Check that in the YAML generation section, Command maps to args
    local has_command_to_args=$(grep -B 5 'args:' "$transformer" | grep -q 'svc\.Command\|Command' && echo "yes" || echo "no")
    local has_entrypoint_to_command=$(grep -B 5 '"command:' "$transformer" | grep -q 'svc\.Entrypoint\|Entrypoint' && echo "yes" || echo "no")

    # Check for WRONG pattern: Command directly to "command:" in YAML
    # This would be in the generateDeployment or similar function
    local wrong_command=$(awk '
        # Look for Command -> command: pattern (wrong)
        /svc\.Command/ { found_command = NR }
        /"        command:"/ && found_command && (NR - found_command) < 10 {
            print NR ": Potential wrong mapping - Command to command:"
            found_command = 0
        }
        # Reset if we see args: (correct pattern)
        /"        args:"/ { found_command = 0 }
    ' "$transformer")

    if [ -n "$wrong_command" ]; then
        # Double check - might be false positive
        # Verify by checking if args: appears after Command handling
        if ! grep -A 10 'svc\.Command' "$transformer" | grep -q '"args:'; then
            echo "WARNING: Possible incorrect Command->command mapping"
            echo "$wrong_command"
            ((WARNINGS++))
        fi
    fi

    # Positive check: verify correct pattern exists
    if grep -q 'Command -> K8s args' "$transformer"; then
        echo "INFO: Found correct Command->args comment"
    fi

    return 0
}

if ! check_command_args_mapping; then
    ERRORS=$((ERRORS + 1))
fi

# =============================================================================
# Check 2: Services for DNS resolution
# =============================================================================
# Every Deployment should have a corresponding Service so pods can reach each
# other by service name. Without Services, DNS resolution fails.
#
# Check that generateManifests creates Services for all services, not just
# those with ports defined.

check_services_for_dns() {
    local transformer="pkg/transform/transformer.go"
    if [ ! -f "$transformer" ]; then
        return 0
    fi

    # Look for conditional Service generation that might skip services without ports
    # Bad pattern: only creating Service when ports are defined
    # Good pattern: creating Service for all deployments (with default port if needed)

    # Check if there's logic that creates Services for all services
    local service_gen=$(grep -n 'generateService\|kind: Service' "$transformer" | head -5)

    if [ -z "$service_gen" ]; then
        echo "WARNING: No Service generation found in transformer"
        echo "  Every Deployment should have a Service for DNS resolution"
        ((WARNINGS++))
        return 0
    fi

    # Check for pattern that skips Service creation when no ports
    # Bad: if len(svc.Ports) > 0 { generateService... }
    local skip_no_ports=$(grep -B 5 -A 5 'generateService' "$transformer" | grep -E 'len\(.*Ports.*\) > 0|len\(.*Ports.*\) == 0.*continue')

    # This is just informational - the actual logic might be correct
    if [ -n "$skip_no_ports" ]; then
        echo "INFO: Found port-conditional Service generation"
        echo "  Ensure services without ports still get ClusterIP Services for DNS"
    fi

    return 0
}

check_services_for_dns

# =============================================================================
# Check 3: Named Docker volumes for K3s
# =============================================================================
# K3s data should use named Docker volumes, not bind mounts.
# Bind mounts fail when kappal runs inside a container (path translation issues).
#
# Bad:  -v /path/to/k3s-data:/var/lib/rancher/k3s
# Good: -v kappal-xxx-k3s-data:/var/lib/rancher/k3s

check_k3s_volume_pattern() {
    local manager="pkg/k3s/manager.go"
    if [ ! -f "$manager" ]; then
        return 0
    fi

    # Check for bind mount pattern (path containing / before :)
    # Bad: k3sDataDir + ":/var/lib/rancher/k3s"
    # Good: k3sDataVolume + ":/var/lib/rancher/k3s"

    local bind_mount=$(grep -n '"/var/lib/rancher/k3s"' "$manager" | grep -v 'Volume')

    # Check if there's a volume name function
    local has_volume_name=$(grep -q 'getK3sDataVolumeName\|VolumeName' "$manager" && echo "yes" || echo "no")

    if [ "$has_volume_name" = "no" ]; then
        echo "WARNING: No volume name generation found in k3s manager"
        echo "  K3s should use named Docker volumes for data persistence"
        echo "  This allows kappal to work when running inside a container"
        ((WARNINGS++))
    fi

    # Check for filepath.Join to k3s-data (indicates bind mount)
    local filepath_k3s=$(grep 'filepath.Join.*k3s-data' "$manager" | grep -v 'MkdirAll')
    if [ -n "$filepath_k3s" ]; then
        # Check if it's used for volume mount
        local used_for_mount=$(grep -A 5 'k3s-data' "$manager" | grep ':/var/lib/rancher')
        if [ -n "$used_for_mount" ]; then
            echo "WARNING: k3s-data path used for volume mount"
            echo "  Consider using named Docker volumes instead of bind mounts"
            echo "  Found: $filepath_k3s"
            ((WARNINGS++))
        fi
    fi

    return 0
}

check_k3s_volume_pattern

# =============================================================================
# Check 4: Host networking for K3s
# =============================================================================
# K3s needs --network host for ServiceLB to work correctly

check_k3s_networking() {
    local manager="pkg/k3s/manager.go"
    if [ ! -f "$manager" ]; then
        return 0
    fi

    if ! grep -q '"--network".*"host"\|"--network=host"' "$manager"; then
        if grep -q 'docker.*run' "$manager"; then
            echo "WARNING: K3s container may not be using host networking"
            echo "  ServiceLB requires --network host to bind to host ports"
            ((WARNINGS++))
        fi
    fi

    return 0
}

check_k3s_networking

# =============================================================================
# Summary
# =============================================================================

echo ""
if [ $ERRORS -gt 0 ]; then
    echo "FAILED: Found $ERRORS error(s) and $WARNINGS warning(s)"
    exit 1
fi

if [ $WARNINGS -gt 0 ]; then
    echo "PASSED with $WARNINGS warning(s)"
else
    echo "OK: All Kubernetes patterns look correct"
fi
