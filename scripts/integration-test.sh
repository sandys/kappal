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

replicas=$(docker exec kappal-k3s kubectl get deploy -n scaling app -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
if [ "$replicas" = "3" ]; then
    echo -e "${GREEN}PASS${NC}: 3 replicas running"
else
    echo -e "${RED}FAIL${NC}: Expected 3 replicas, got $replicas"
    docker exec kappal-k3s kubectl get pods -n scaling
fi

kappal down -v

echo ""
echo "=== Test 3: Volume Persistence ==="
cd "$PROJECT_DIR/testdata/volume"

kappal up -d
sleep 10

# Write data
docker exec kappal-k3s kubectl exec -n volume deploy/app -- sh -c 'echo "persist-test" > /data/test.txt'

# Restart pod
docker exec kappal-k3s kubectl delete pod -n volume -l kappal.io/service=app
sleep 10

# Check data
if docker exec kappal-k3s kubectl exec -n volume deploy/app -- cat /data/test.txt 2>/dev/null | grep -q "persist-test"; then
    echo -e "${GREEN}PASS${NC}: Volume data persisted"
else
    echo -e "${RED}FAIL${NC}: Volume data not persisted"
fi

kappal down -v

echo ""
echo "=== All tests completed! ==="
