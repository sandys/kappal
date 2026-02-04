#!/bin/bash
# Ensure kubectl is not exposed in user-facing text
# Users should ONLY interact with `kappal` CLI
set -e

echo "Checking for kubectl exposure in user-facing code..."

ERRORS=0

# Check CLI help text doesn't mention kubectl
if [ -d "cmd/kappal" ]; then
    EXPOSED=$(grep -rn "kubectl" cmd/kappal/*.go 2>/dev/null | grep -E '(Short|Long|Use|Example).*kubectl' || true)
    if [ -n "$EXPOSED" ]; then
        echo "ERROR: kubectl mentioned in CLI help text:"
        echo "$EXPOSED"
        ERRORS=$((ERRORS + 1))
    fi
fi

# Check error messages don't suggest kubectl to users
if [ -d "pkg" ] || [ -d "cmd" ]; then
    EXPOSED=$(grep -rn 'fmt\.\(Print\|Error\|Sprintf\).*kubectl' pkg/ cmd/ 2>/dev/null | grep -v "_test.go" || true)
    if [ -n "$EXPOSED" ]; then
        echo "ERROR: kubectl mentioned in user-facing messages:"
        echo "$EXPOSED"
        ERRORS=$((ERRORS + 1))
    fi
fi

# Check test scripts don't expose kubectl to users in actual test code
# Exception: _internal_kubectl function implementation is allowed
# We use awk to find 'docker exec.*kubectl' outside of _internal_kubectl function
check_script_for_kubectl_exposure() {
    local script="$1"
    if [ ! -f "$script" ]; then
        return 0
    fi

    # Use awk to find docker exec kubectl usage outside of _internal_kubectl function
    # The _internal_kubectl function is allowed to contain docker exec kubectl as fallback
    local exposed
    exposed=$(awk '
        # Track if we are inside _internal_kubectl function
        /_internal_kubectl\(\)/ { in_internal = 1 }
        /^}$/ && in_internal { in_internal = 0 }

        # Flag lines with docker exec kubectl that are NOT in _internal_kubectl
        /docker exec.*kubectl/ && !in_internal {
            print NR ":" $0
        }
    ' "$script")

    if [ -n "$exposed" ]; then
        echo "ERROR: $script exposes kubectl (should use 'kappal exec' or _internal_* helpers):"
        echo "$exposed"
        return 1
    fi
    return 0
}

for test_script in scripts/conformance-test.sh scripts/integration-test.sh scripts/dev-test.sh; do
    if ! check_script_for_kubectl_exposure "$test_script"; then
        ERRORS=$((ERRORS + 1))
    fi
done

if [ $ERRORS -gt 0 ]; then
    echo ""
    echo "FAILED: Found $ERRORS kubectl exposure issue(s)"
    echo ""
    echo "Remember: Users should only see 'kappal' commands, never kubectl."
    echo "Use 'kappal exec <service> <command>' instead of 'docker exec ... kubectl exec'"
    exit 1
fi

echo "OK: No kubectl exposure found in user-facing code"
