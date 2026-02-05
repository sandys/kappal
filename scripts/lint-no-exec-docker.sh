#!/bin/bash
# Ensure we don't use exec.Command("docker", ...) in Go code
#
# Anti-pattern: exec.Command("docker", "run", ...)
# Anti-pattern: exec.CommandContext(ctx, "docker", ...)
#
# All Docker operations should use the Docker SDK (pkg/docker/client.go)
# which provides:
#   - Typed errors via errdefs.IsNotFound() etc.
#   - Idempotent operations (Stop/Remove return nil if not found)
#   - No string parsing for container state
#   - Proper streaming I/O
#
set -e

echo "Checking for exec.Command(\"docker\", ...) calls in Go code..."

ERRORS=0

# Search for exec.Command or exec.CommandContext with "docker" as first arg
# Exclude:
#   - This lint script itself
#   - Comments (lines starting with //)
#   - Test files that might be testing the pattern

check_exec_docker() {
    # Use grep to find exec.Command.*"docker" patterns
    # -r recursive, -n line numbers, --include only .go files
    local matches=$(grep -rn --include='*.go' 'exec\.Command.*"docker"' pkg/ cmd/ 2>/dev/null || true)

    if [ -n "$matches" ]; then
        # Filter out comments (lines where exec.Command appears after //)
        local real_matches=""
        while IFS= read -r line; do
            # Extract the file:linenum:content
            local content=$(echo "$line" | cut -d: -f3-)
            # Check if the exec.Command is in a comment
            if [[ ! "$content" =~ ^[[:space:]]*//.* ]]; then
                real_matches="$real_matches$line"$'\n'
            fi
        done <<< "$matches"

        if [ -n "$real_matches" ]; then
            echo "ERROR: Found exec.Command(\"docker\", ...) calls in Go code:"
            echo "$real_matches"
            echo ""
            echo "Use pkg/docker/client.go instead. Examples:"
            echo "  - docker run    -> docker.ContainerRun()"
            echo "  - docker stop   -> docker.ContainerStop()"
            echo "  - docker rm     -> docker.ContainerRemove()"
            echo "  - docker exec   -> docker.ContainerExec() or docker.ContainerExecStream()"
            echo "  - docker build  -> docker.ImageBuild()"
            echo "  - docker save   -> docker.ImageSave()"
            echo "  - docker volume -> docker.VolumeRemove() / docker.VolumeCreate()"
            return 1
        fi
    fi

    return 0
}

if ! check_exec_docker; then
    ERRORS=$((ERRORS + 1))
fi

if [ $ERRORS -gt 0 ]; then
    echo ""
    echo "FAILED: Found $ERRORS file(s) with exec.Command(\"docker\", ...) calls"
    echo ""
    echo "The Docker SDK provides better error handling and avoids string parsing."
    echo "See pkg/docker/client.go for the wrapper API."
    exit 1
fi

echo "OK: No exec.Command(\"docker\", ...) calls found in Go code"
