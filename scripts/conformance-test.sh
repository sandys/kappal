#!/bin/bash
set -e

# Conformance test runner for Kappal
# Tests based on compose-spec/conformance-tests

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
TESTDATA_DIR="$PROJECT_DIR/testdata"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

PASSED=0
FAILED=0
SKIPPED=0

log_pass() {
    echo -e "${GREEN}PASS${NC}: $1"
    ((PASSED++)) || true
}

log_fail() {
    echo -e "${RED}FAIL${NC}: $1"
    ((FAILED++)) || true
}

log_skip() {
    echo -e "${YELLOW}SKIP${NC}: $1"
    ((SKIPPED++)) || true
}

# Ensure we're running in Docker with Docker socket mounted
check_docker() {
    if ! docker info > /dev/null 2>&1; then
        echo "Docker not available. Run with: docker run -v /var/run/docker.sock:/var/run/docker.sock ..."
        exit 1
    fi
}

# Clean up any existing test resources
cleanup() {
    echo "Cleaning up..."
    docker rm -f kappal-k3s 2>/dev/null || true
    # Clean up .kappal directories in test dirs
    for dir in "$TESTDATA_DIR"/*/; do
        rm -rf "${dir}.kappal" 2>/dev/null || true
    done
}

# Detect if kappal binary is available locally
KAPPAL_DIRECT=false
if command -v kappal &> /dev/null; then
    KAPPAL_DIRECT=true
fi

# Run kappal command - either directly or via Docker
kappal() {
    if [ "$KAPPAL_DIRECT" = true ]; then
        /usr/local/bin/kappal "$@"
    else
        docker run --rm \
            -v /var/run/docker.sock:/var/run/docker.sock \
            -v "$PWD:/project" \
            -w /project \
            --network host \
            kappal:latest "$@"
    fi
}

# Test: Simple lifecycle (up/down)
test_simple_lifecycle() {
    local test_name="SimpleLifecycle"
    local test_dir="$TESTDATA_DIR/simple"

    echo "Running test: $test_name"
    cd "$test_dir"

    # Up
    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 5

    # Check status
    if ! kappal ps | grep -q "Up"; then
        log_fail "$test_name - service not running"
        kappal down -v
        return
    fi

    # Down
    if ! kappal down -v; then
        log_fail "$test_name - kappal down failed"
        return
    fi

    log_pass "$test_name"
}

# Test: Service-to-service networking
test_simple_network() {
    local test_name="SimpleNetwork"
    local test_dir="$TESTDATA_DIR/network"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 10

    # Test that frontend can reach backend by service name
    if docker exec kappal-k3s kubectl exec -n network deploy/frontend -- \
        wget -q -O - http://backend:8080 2>/dev/null | grep -q "OK"; then
        log_pass "$test_name"
    else
        log_fail "$test_name - service-to-service communication failed"
    fi

    kappal down -v
}

# Test: Volume persistence
test_volume_file() {
    local test_name="VolumeFile"
    local test_dir="$TESTDATA_DIR/volume"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 5

    # Write data to volume
    docker exec kappal-k3s kubectl exec -n volume deploy/app -- \
        sh -c 'echo "test-data" > /data/testfile'

    # Restart deployment
    docker exec kappal-k3s kubectl rollout restart -n volume deploy/app
    sleep 10

    # Check data persists
    if docker exec kappal-k3s kubectl exec -n volume deploy/app -- \
        cat /data/testfile 2>/dev/null | grep -q "test-data"; then
        log_pass "$test_name"
    else
        log_fail "$test_name - volume data not persisted"
    fi

    kappal down -v
}

# Test: Secrets
test_secret_file() {
    local test_name="SecretFile"
    local test_dir="$TESTDATA_DIR/secret"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 5

    # Check secret is mounted at /run/secrets/
    if docker exec kappal-k3s kubectl exec -n secret deploy/app -- \
        cat /run/secrets/my_secret 2>/dev/null | grep -q "secret-value"; then
        log_pass "$test_name"
    else
        log_fail "$test_name - secret not accessible"
    fi

    kappal down -v
}

# Test: Config files
test_config_file() {
    local test_name="ConfigFile"
    local test_dir="$TESTDATA_DIR/config"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 5

    # Check config is mounted
    if docker exec kappal-k3s kubectl exec -n config deploy/app -- \
        cat /etc/app/config.json 2>/dev/null | grep -q "setting"; then
        log_pass "$test_name"
    else
        log_fail "$test_name - config not accessible"
    fi

    kappal down -v
}

# Test: UDP ports
test_udp_port() {
    local test_name="UdpPort"
    local test_dir="$TESTDATA_DIR/udp"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 5

    # Check service has UDP port
    if docker exec kappal-k3s kubectl get svc -n udp dns -o yaml | grep -q "protocol: UDP"; then
        log_pass "$test_name"
    else
        log_fail "$test_name - UDP port not configured"
    fi

    kappal down -v
}

# Test: Scaling with replicas
test_scaling() {
    local test_name="Scaling"
    local test_dir="$TESTDATA_DIR/scaling"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 10

    # Check replicas
    replicas=$(docker exec kappal-k3s kubectl get deploy -n scaling app -o jsonpath='{.spec.replicas}')
    if [ "$replicas" = "3" ]; then
        log_pass "$test_name"
    else
        log_fail "$test_name - expected 3 replicas, got $replicas"
    fi

    kappal down -v
}

# Test: Network isolation
test_different_networks() {
    local test_name="DifferentNetworks"
    local test_dir="$TESTDATA_DIR/networks"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 10

    # Check NetworkPolicy exists
    if docker exec kappal-k3s kubectl get networkpolicy -n networks frontend-net -o name 2>/dev/null; then
        log_pass "$test_name"
    else
        log_fail "$test_name - NetworkPolicy not created"
    fi

    kappal down -v
}

# Main
main() {
    echo "=========================================="
    echo "Kappal Conformance Tests"
    echo "=========================================="

    check_docker
    cleanup

    trap cleanup EXIT

    # Run tests
    test_simple_lifecycle
    test_simple_network
    test_volume_file
    test_secret_file
    test_config_file
    test_udp_port
    test_scaling
    test_different_networks

    echo ""
    echo "=========================================="
    echo "Results: ${GREEN}$PASSED passed${NC}, ${RED}$FAILED failed${NC}, ${YELLOW}$SKIPPED skipped${NC}"
    echo "=========================================="

    if [ $FAILED -gt 0 ]; then
        exit 1
    fi
}

main "$@"
