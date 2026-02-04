#!/bin/bash
# Lint for volume persistence patterns
#
# Key principle: `kappal down` (without -v) should preserve volumes
# Only `kappal down -v` should delete volumes
#
# This means:
# 1. tanka.Delete should NOT delete the namespace by default (namespace deletion cascades to PVCs)
# 2. Only when DeleteVolumes=true should the namespace be deleted
# 3. K3s data volume should persist across down/up cycles
#
set -e

echo "Checking for volume persistence patterns..."

ERRORS=0

# =============================================================================
# Check 1: tanka.Delete should preserve namespace by default
# =============================================================================

check_tanka_delete() {
    local apply_file="pkg/tanka/apply.go"
    if [ ! -f "$apply_file" ]; then
        echo "WARNING: pkg/tanka/apply.go not found"
        return 0
    fi

    # The Delete function should:
    # - When DeleteVolumes=false: delete deployments,services,etc but NOT namespace
    # - When DeleteVolumes=true: delete the entire namespace

    # Check for DeleteVolumes option
    if ! grep -q 'DeleteVolumes' "$apply_file"; then
        echo "ERROR: DeleteVolumes option not found in tanka/apply.go"
        echo "  The Delete function should accept DeleteVolumes to control namespace deletion"
        return 1
    fi

    # Check that namespace deletion is conditional
    local namespace_delete=$(grep -A 30 'func Delete' "$apply_file" | grep -n 'delete.*namespace')
    if [ -z "$namespace_delete" ]; then
        echo "WARNING: No namespace deletion found in Delete function"
        echo "  Verify that DeleteVolumes=true triggers namespace deletion"
    fi

    # Check that default case does NOT delete namespace
    local default_case=$(grep -A 30 'func Delete' "$apply_file" | grep -E 'delete.*deployments|delete.*services')
    if [ -z "$default_case" ]; then
        echo "WARNING: No selective resource deletion found"
        echo "  Without DeleteVolumes, only delete deployments/services/etc, not namespace"
    fi

    return 0
}

if ! check_tanka_delete; then
    ERRORS=$((ERRORS + 1))
fi

# =============================================================================
# Check 2: down.go should preserve volumes by default
# =============================================================================

check_down_command() {
    local down_file="cmd/kappal/down.go"
    if [ ! -f "$down_file" ]; then
        echo "WARNING: cmd/kappal/down.go not found"
        return 0
    fi

    # Check for --volumes or -v flag
    if ! grep -q 'volumes.*bool\|BoolVarP.*volumes' "$down_file"; then
        echo "ERROR: --volumes flag not found in down.go"
        echo "  Users need -v/--volumes flag to opt-in to volume deletion"
        return 1
    fi

    # Check that DeleteVolumes is passed to tanka.Delete
    if ! grep -q 'DeleteVolumes' "$down_file"; then
        echo "ERROR: DeleteVolumes not passed to Delete in down.go"
        echo "  The down command should pass DeleteVolumes based on --volumes flag"
        return 1
    fi

    # Check that CleanRuntime is conditional on volumes flag
    local clean_runtime=$(grep -B 5 'CleanRuntime' "$down_file" | grep -E 'if.*[Vv]olumes')
    if [ -z "$clean_runtime" ]; then
        echo "WARNING: CleanRuntime might not be conditional on --volumes flag"
        echo "  Only remove runtime data when -v/--volumes is specified"
    fi

    return 0
}

if ! check_down_command; then
    ERRORS=$((ERRORS + 1))
fi

# =============================================================================
# Check 3: K3s manager CleanRuntime should clean volumes
# =============================================================================

check_k3s_cleanup() {
    local manager="pkg/k3s/manager.go"
    if [ ! -f "$manager" ]; then
        return 0
    fi

    # CleanRuntime should remove Docker volumes
    local clean_volumes=$(grep -A 10 'func.*CleanRuntime' "$manager" | grep -E 'volume.*rm|docker.*volume')
    if [ -z "$clean_volumes" ]; then
        echo "WARNING: CleanRuntime may not be removing Docker volumes"
        echo "  When using named volumes, CleanRuntime should: docker volume rm <volume>"
    fi

    return 0
}

check_k3s_cleanup

# =============================================================================
# Summary
# =============================================================================

echo ""
if [ $ERRORS -gt 0 ]; then
    echo "FAILED: Found $ERRORS error(s)"
    echo ""
    echo "Volume persistence principle:"
    echo "  kappal down     -> Stop services, K3s; KEEP volumes/PVCs"
    echo "  kappal down -v  -> Stop services, K3s; DELETE volumes/PVCs"
    exit 1
fi

echo "OK: Volume persistence patterns look correct"
