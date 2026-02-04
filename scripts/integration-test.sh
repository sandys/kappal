#!/bin/bash
set -e

# Integration test for Kappal
# Runs a complete end-to-end test

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

cleanup() {
    echo "Cleaning up..."
    docker rm -f kappal-k3s 2>/dev/null || true
}

trap cleanup EXIT

kappal() {
    docker run --rm \
        -v /var/run/docker.sock:/var/run/docker.sock \
        -v "$PWD:/project" \
        -w /project \
        --network host \
        kappal:latest "$@"
}

# =============================================================================
# INTERNAL HELPERS FOR IMPLEMENTATION VALIDATION
# These functions verify Kubernetes internals. They are NOT user-facing and
# should NEVER be exposed to users. Users only interact with kappal commands.
# =============================================================================

# Internal: Get kubeconfig path from workspace
_internal_kubeconfig() {
    echo "$PWD/.kappal/runtime/kubeconfig.yaml"
}

# Internal: Run kubectl command against kappal's K8s cluster
# This is for INTERNAL testing only - users never see kubectl
_internal_kubectl() {
    local kubeconfig=$(_internal_kubeconfig)
    if [ -f "$kubeconfig" ]; then
        kubectl --kubeconfig="$kubeconfig" "$@" 2>/dev/null
    else
        # Fallback to docker exec if kubeconfig not available locally
        docker exec kappal-k3s kubectl "$@" 2>/dev/null
    fi
}

# Internal: Get ready replica count from a K8s Deployment
_internal_get_ready_replicas() {
    local namespace="$1"
    local deployment="$2"
    _internal_kubectl get deploy -n "$namespace" "$deployment" -o jsonpath='{.status.readyReplicas}' || echo "0"
}

# Internal: Get pods for debugging
_internal_get_pods() {
    local namespace="$1"
    _internal_kubectl get pods -n "$namespace"
}

# Internal: Delete pods by label (to test restart)
_internal_delete_pods() {
    local namespace="$1"
    local label="$2"
    _internal_kubectl delete pod -n "$namespace" -l "$label"
}

# =============================================================================

echo "=== Building Kappal ==="
cd "$PROJECT_DIR"
docker build -f Dockerfile.build -t kappal:latest .

echo ""
echo "=== Test 1: Simple Lifecycle ==="
cd "$PROJECT_DIR/testdata/simple"
cleanup

kappal up -d
sleep 10

if kappal ps | grep -q "Up"; then
    echo -e "${GREEN}PASS${NC}: Service is running"
else
    echo -e "${RED}FAIL${NC}: Service not running"
    kappal ps
    exit 1
fi

# Test that we can reach the nginx
if curl -s http://localhost:8080 | grep -q "nginx"; then
    echo -e "${GREEN}PASS${NC}: Service is accessible on port 8080"
else
    echo -e "${RED}FAIL${NC}: Service not accessible"
    exit 1
fi

kappal down -v
echo -e "${GREEN}PASS${NC}: Down completed"

echo ""
echo "=== Test 2: Scaling ==="
cd "$PROJECT_DIR/testdata/scaling"

kappal up -d
sleep 15

# Internal verification: check K8s Deployment has correct ready replicas
replicas=$(_internal_get_ready_replicas "scaling" "app")
if [ "$replicas" = "3" ]; then
    echo -e "${GREEN}PASS${NC}: 3 replicas running"
else
    echo -e "${RED}FAIL${NC}: Expected 3 replicas, got $replicas"
    _internal_get_pods "scaling"
fi

kappal down -v

echo ""
echo "=== Test 3: Volume Persistence ==="
cd "$PROJECT_DIR/testdata/volume"

kappal up -d
sleep 10

# Write data using kappal exec (mirrors docker compose exec)
kappal exec app sh -c 'echo "persist-test" > /data/test.txt'

# Restart by doing down/up to test persistence
kappal down
kappal up -d
sleep 10

# Check data using kappal exec
if kappal exec app cat /data/test.txt 2>/dev/null | grep -q "persist-test"; then
    echo -e "${GREEN}PASS${NC}: Volume data persisted"
else
    echo -e "${RED}FAIL${NC}: Volume data not persisted"
fi

kappal down -v

echo ""
echo "=== All tests completed! ==="
