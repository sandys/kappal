#!/bin/bash
# Lint for kappal compatibility mode guardrails.
#
# Goal: third-party docker-compose projects should run on kappal without patching.
# These checks ensure:
# 1) up path runs compatibility analysis
# 2) writable bind mounts trigger permission-prep init flow
# 3) kappal-init understands prepareWritablePaths
# 4) regression tests exist for checker logic
set -e

echo "Checking compatibility mode guardrails..."

ERRORS=0

require_pattern() {
    local file="$1"
    local pattern="$2"
    local message="$3"
    if ! grep -qE "$pattern" "$file"; then
        echo "ERROR: $message"
        echo "  Missing pattern: $pattern"
        echo "  File: $file"
        ERRORS=$((ERRORS + 1))
    fi
}

# up.go must run compatibility analysis and print findings.
require_pattern "cmd/kappal/up.go" 'analyzeCompatibility\(project\)' \
    "runUp must analyze compatibility before deployment"
require_pattern "cmd/kappal/up.go" 'Compatibility check:' \
    "runUp should surface compatibility findings to users"
require_pattern "cmd/kappal/up.go" 'compat\.NeedInitImage' \
    "init image loading should be driven by compatibility report"

# transformer must emit writable-path prep into KAPPAL_INIT_SPEC.
require_pattern "pkg/transform/transformer.go" '"prepareWritablePaths"' \
    "transformer must include prepareWritablePaths in init spec"
require_pattern "pkg/transform/transformer.go" 'runAsUser:\s*0' \
    "init container for bind-mount prep must run as root"

# kappal-init must parse and execute writable path prep.
require_pattern "cmd/kappal-init/main.go" 'PrepareWritablePaths' \
    "kappal-init InitSpec must include PrepareWritablePaths"
require_pattern "cmd/kappal-init/main.go" 'prepareWritablePaths\(' \
    "kappal-init must execute prepareWritablePaths during startup"

# Ensure test coverage exists for these compatibility paths.
require_pattern "cmd/kappal/up_test.go" 'TestShouldLoadInitImage' \
    "missing tests for init-image compatibility trigger logic"
require_pattern "cmd/kappal-init/main_test.go" 'TestPrepareWritablePaths' \
    "missing tests for writable path preparation"
require_pattern "pkg/transform/transformer_test.go" 'TestInitContainerWritableBindMounts' \
    "missing transformer tests for writable bind init generation"

if [ "$ERRORS" -gt 0 ]; then
    echo ""
    echo "FAILED: Found $ERRORS compatibility mode issue(s)"
    exit 1
fi

echo "OK: Compatibility mode guardrails look good"
