#!/bin/bash
# Kappal wrapper script - runs kappal via Docker with proper path mapping
#
# Installation:
#   1. Copy to your PATH:  sudo cp scripts/kappal-wrapper.sh /usr/local/bin/kappal
#   2. Make executable:    sudo chmod +x /usr/local/bin/kappal
#   3. Source completion:  source scripts/kappal-completion.bash
#      (add to ~/.bashrc for persistence)

# Find project root by looking for docker-compose.yaml/yml
find_project_root() {
    local dir="$1"
    local compose_file="$2"

    # If compose file is specified and is absolute, use its directory
    if [[ "$compose_file" == /* ]]; then
        dirname "$compose_file"
        return
    fi

    # If compose file is relative, check if it contains path components
    if [[ "$compose_file" == */* ]]; then
        # Has directory component - project root is current dir
        echo "$dir"
        return
    fi

    # Search upward for docker-compose.yaml or docker-compose.yml
    while [[ "$dir" != "/" ]]; do
        if [[ -f "$dir/docker-compose.yaml" ]] || [[ -f "$dir/docker-compose.yml" ]]; then
            echo "$dir"
            return
        fi
        dir="$(dirname "$dir")"
    done

    # Default to current directory
    echo "$(pwd)"
}

# Parse arguments to find -f/--file flag
parse_compose_file() {
    local next_is_file=false
    for arg in "$@"; do
        if $next_is_file; then
            echo "$arg"
            return
        fi
        case "$arg" in
            -f|--file)
                next_is_file=true
                ;;
            -f=*|--file=*)
                echo "${arg#*=}"
                return
                ;;
        esac
    done
}

# Main
compose_file=$(parse_compose_file "$@")
current_dir="$(pwd)"

# Determine project root and working directory
if [[ -n "$compose_file" ]]; then
    compose_dir="$(dirname "$compose_file")"
    if [[ "$compose_dir" == "." ]]; then
        project_root="$current_dir"
        work_dir="/project"
    else
        # Compose file is in a subdirectory - mount from current dir
        project_root="$current_dir"
        work_dir="/project/$compose_dir"
    fi
else
    project_root="$current_dir"
    work_dir="/project"
fi

# Convert compose file path to container path
container_args=()
next_is_file=false
for arg in "$@"; do
    if $next_is_file; then
        # Convert to just the filename since we set work_dir
        container_args+=("$(basename "$arg")")
        next_is_file=false
        continue
    fi
    case "$arg" in
        -f|--file)
            container_args+=("$arg")
            next_is_file=true
            ;;
        -f=*|--file=*)
            prefix="${arg%%=*}"
            file="${arg#*=}"
            container_args+=("$prefix=$(basename "$file")")
            ;;
        *)
            container_args+=("$arg")
            ;;
    esac
done

exec docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$project_root:/project" \
    -w "$work_dir" \
    -e KAPPAL_HOST_DIR="$project_root" \
    --network host \
    kappal:latest "${container_args[@]}"
