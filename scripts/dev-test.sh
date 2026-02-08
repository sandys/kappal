#!/bin/bash
set -e

# Quick development test script
# Tests basic kappal functionality with a simple compose file

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Building Kappal ==="
cd "$PROJECT_DIR"
docker build -t kappal:latest .

echo ""
echo "=== Running simple test ==="
cd "$PROJECT_DIR/testdata/simple"

# Clean up any existing kappal containers and networks
docker ps -a --filter "name=kappal-" -q | xargs -r docker rm -f 2>/dev/null || true
docker network ls --filter "name=kappal-" -q | xargs -r docker network rm 2>/dev/null || true

# Run setup first
echo ""
echo "=== Running kappal setup ==="
docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$PWD:/project" \
    -w /project \
    -e KAPPAL_HOST_DIR="$PWD" \
    --network host \
    kappal:latest --setup

# Run kappal up
docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$PWD:/project" \
    -w /project \
    -e KAPPAL_HOST_DIR="$PWD" \
    --network host \
    kappal:latest up -d

echo ""
echo "=== Checking status ==="
sleep 5
docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$PWD:/project" \
    -w /project \
    -e KAPPAL_HOST_DIR="$PWD" \
    --network host \
    kappal:latest ps

echo ""
echo "=== Cleaning up ==="
docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "$PWD:/project" \
    -w /project \
    -e KAPPAL_HOST_DIR="$PWD" \
    --network host \
    kappal:latest down -v

echo ""
echo "=== Test completed ==="
