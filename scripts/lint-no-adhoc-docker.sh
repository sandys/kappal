#!/bin/bash
# Ensure we don't use ad-hoc Docker commands for things that should be kappal commands
#
# Anti-pattern: Using "docker run ... rm -rf .kappal" instead of "kappal clean"
# Anti-pattern: Using "docker exec kappal-k3s ..." instead of kappal commands
#
# If you find yourself needing a Docker workaround, BUILD IT INTO KAPPAL.
set -e

echo "Checking for ad-hoc Docker commands that should be kappal commands..."

ERRORS=0

# Check scripts for ad-hoc Docker cleanup commands
check_adhoc_docker() {
    local file="$1"
    if [ ! -f "$file" ]; then
        return 0
    fi

    # Skip lint scripts themselves (they contain examples of anti-patterns in comments)
    if [[ "$file" == *"lint-"* ]]; then
        return 0
    fi

    # Pattern 1: docker run ... rm -rf .kappal (should be kappal clean)
    local cleanup_pattern=$(grep -n 'docker run.*rm.*\.kappal' "$file" 2>/dev/null || true)
    if [ -n "$cleanup_pattern" ]; then
        echo "ERROR: $file uses ad-hoc Docker to clean .kappal (use 'kappal clean' instead):"
        echo "$cleanup_pattern"
        return 1
    fi

    # Pattern 2: docker run ... alpine/busybox for file operations (should be built into kappal)
    local alpine_ops=$(grep -n 'docker run.*\(alpine\|busybox\).*\(rm\|mv\|cp\|chmod\|chown\)' "$file" 2>/dev/null || true)
    if [ -n "$alpine_ops" ]; then
        echo "ERROR: $file uses ad-hoc Docker for file operations (build into kappal instead):"
        echo "$alpine_ops"
        return 1
    fi

    return 0
}

# Check all shell scripts
for script in scripts/*.sh; do
    if [ -f "$script" ]; then
        if ! check_adhoc_docker "$script"; then
            ERRORS=$((ERRORS + 1))
        fi
    fi
done

# Also check test scripts if they exist
for script in test/*.sh tests/*.sh; do
    if [ -f "$script" ]; then
        if ! check_adhoc_docker "$script"; then
            ERRORS=$((ERRORS + 1))
        fi
    fi
done

if [ $ERRORS -gt 0 ]; then
    echo ""
    echo "FAILED: Found $ERRORS ad-hoc Docker command(s)"
    echo ""
    echo "If you need a Docker workaround, BUILD IT INTO KAPPAL:"
    echo "  - Need to clean up? Use 'kappal clean' (add the feature if missing)"
    echo "  - Need file operations? Add to pkg/workspace or pkg/k3s"
    echo "  - Need kubectl? Add to pkg/k8s (never expose kubectl to users)"
    exit 1
fi

echo "OK: No ad-hoc Docker commands found"
