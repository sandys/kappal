#!/bin/bash
# Ensure compose-go features are fully implemented
# When we use compose-go fields, we should support them through the entire pipeline
set -e

echo "Checking compose-go feature coverage..."

ERRORS=0

# Check that build.dockerfile is passed when BuildImage is called
# If a service has Build with Dockerfile field, we need to pass it to BuildImage

# Pattern: if we call BuildImage without a dockerfile parameter when Dockerfile field exists
check_build_calls() {
    local file="$1"

    # Look for BuildImage calls that reference Build.Context but not dockerfile
    # This catches the pattern: BuildImage(ctx, project.Name, svc.Name, svc.Build.Context)
    # when it should be: BuildImage(ctx, project.Name, svc.Name, svc.Build.Context, dockerfile)

    # Extract lines with BuildImage calls
    local buildimage_lines=$(grep -n 'BuildImage' "$file" 2>/dev/null || true)
    if [ -z "$buildimage_lines" ]; then
        return 0
    fi

    # Check if any BuildImage call has Build.Context but not dockerfile nearby
    # The correct pattern is to have dockerfile variable defined and passed
    local has_context=$(grep 'BuildImage.*Build\.Context' "$file" 2>/dev/null || true)
    local has_dockerfile_var=$(grep -E '^\s*dockerfile\s*:?=' "$file" 2>/dev/null || true)

    if [ -n "$has_context" ] && [ -z "$has_dockerfile_var" ]; then
        echo "WARNING: $file calls BuildImage with Build.Context but doesn't handle Dockerfile:"
        echo "$has_context"
        echo "  -> When calling BuildImage, also check svc.Build.Dockerfile and pass it"
        return 1
    fi
    return 0
}

# Check all Go files that might call BuildImage
for gofile in cmd/kappal/*.go; do
    if [ -f "$gofile" ]; then
        if grep -q "BuildImage" "$gofile" 2>/dev/null; then
            if ! check_build_calls "$gofile"; then
                ERRORS=$((ERRORS + 1))
            fi
        fi
    fi
done

# Check that compose-spec fields accessed in transformer are used downstream
# Specifically: if transformer.go references a Build.Dockerfile, the build code must use it
check_transformer_coverage() {
    local transformer_file="pkg/transform/transformer.go"
    local manager_file="pkg/k3s/manager.go"

    if [ ! -f "$transformer_file" ] || [ ! -f "$manager_file" ]; then
        return 0
    fi

    # Check for dockerfile field in transformer
    if grep -q 'Build\.Dockerfile' "$transformer_file" || grep -q 'Dockerfile.*string' "$transformer_file"; then
        # Ensure manager supports dockerfile
        if ! grep -q 'dockerfile.*string' "$manager_file"; then
            echo "ERROR: Transformer handles Dockerfile but manager.go doesn't accept it"
            return 1
        fi
    fi

    return 0
}

if ! check_transformer_coverage; then
    ERRORS=$((ERRORS + 1))
fi

# Check that compose-go struct fields we define have corresponding usage
check_spec_field_usage() {
    local field="$1"
    local spec_file="pkg/transform/transformer.go"

    # If we define a field in our spec structs, ensure it's used in manifest generation
    if grep -qE "^\s+${field}\s+" "$spec_file" 2>/dev/null; then
        # Check if it's used in generateDeployment or generateService
        if ! grep -q "$field" "$spec_file" | grep -q -E '(generate|manifest)'; then
            # This is just informational, not an error
            echo "INFO: Spec field '$field' defined but check usage in manifest generation"
        fi
    fi
}

if [ $ERRORS -gt 0 ]; then
    echo ""
    echo "FAILED: Found $ERRORS compose feature coverage issue(s)"
    echo ""
    echo "When using compose-go features:"
    echo "  1. All compose-spec fields that are parsed should be passed through the pipeline"
    echo "  2. BuildImage should receive dockerfile param when the compose file specifies one"
    echo "  3. Transformer fields should be used in manifest generation"
    exit 1
fi

echo "OK: Compose-go feature coverage looks good"
