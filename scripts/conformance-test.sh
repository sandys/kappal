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
        docker exec "kappal-$(basename "$PWD")-k3s" kubectl "$@" 2>/dev/null
    fi
}

# Internal: Verify a K8s Service has UDP protocol configured
_internal_verify_udp_service() {
    local namespace="$1"
    local service="$2"
    _internal_kubectl get svc -n "$namespace" "$service" -o yaml | grep -q "protocol: UDP"
}

# Internal: Get replica count from a K8s Deployment
_internal_get_replicas() {
    local namespace="$1"
    local deployment="$2"
    _internal_kubectl get deploy -n "$namespace" "$deployment" -o jsonpath='{.spec.replicas}'
}

# Internal: Verify a K8s NetworkPolicy exists
_internal_verify_networkpolicy() {
    local namespace="$1"
    local policy="$2"
    _internal_kubectl get networkpolicy -n "$namespace" "$policy" -o name
}

# Internal: Get a K8s resource kind for a named resource
_internal_get_resource_kind() {
    local namespace="$1"
    local name="$2"
    if _internal_kubectl get job -n "$namespace" "$name" -o name 2>/dev/null | grep -q "job"; then
        echo "Job"
    elif _internal_kubectl get deploy -n "$namespace" "$name" -o name 2>/dev/null | grep -q "deployment"; then
        echo "Deployment"
    else
        echo "Unknown"
    fi
}

# Internal: Get pod phase for a service
_internal_get_pod_phase() {
    local namespace="$1"
    local label="$2"
    _internal_kubectl get pod -n "$namespace" -l "$label" -o jsonpath='{.items[0].status.phase}' 2>/dev/null
}

# Internal: Check if a pod has an init container with a given name
_internal_has_init_container() {
    local namespace="$1"
    local label="$2"
    local init_name="$3"
    _internal_kubectl get pod -n "$namespace" -l "$label" -o jsonpath='{.items[0].spec.initContainers[*].name}' 2>/dev/null | grep -q "$init_name"
}

# Internal: Get restart policy for a pod
_internal_get_restart_policy() {
    local namespace="$1"
    local label="$2"
    _internal_kubectl get pod -n "$namespace" -l "$label" -o jsonpath='{.items[0].spec.restartPolicy}' 2>/dev/null
}

# =============================================================================

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
    # Remove all kappal K3s containers and networks
    docker ps -a --filter "name=kappal-" -q | xargs -r docker rm -f 2>/dev/null || true
    docker network ls --filter "name=kappal-" -q | xargs -r docker network rm 2>/dev/null || true
    # Clean up kappal Docker volumes
    docker volume ls -q | grep '^kappal-' | xargs -r docker volume rm 2>/dev/null || true
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
            kappal:latest -p "$(basename "$PWD")" "$@"
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

    # Wait for pods to be ready and DNS to stabilize
    sleep 15

    # Look up the backend's container port dynamically via kappal inspect
    BACKEND_PORT=$(kappal inspect | jq -r '.services[] | select(.name=="backend") | .ports[0].container')
    if [ -z "$BACKEND_PORT" ] || [ "$BACKEND_PORT" = "null" ]; then
        log_fail "$test_name - could not determine backend port from kappal inspect"
        kappal down -v
        return
    fi

    # Test that frontend can reach backend by service name
    # Using kappal exec mirrors docker compose exec experience
    # Use nc for raw TCP test since busybox wget has strict HTTP parsing
    if kappal exec frontend sh -c "echo 'GET /' | nc -w 5 backend $BACKEND_PORT" 2>/dev/null | grep -q "OK"; then
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

    # Wait for pod to be fully ready
    sleep 10

    # Write data to volume using kappal exec
    kappal exec app sh -c 'echo "test-data" > /data/testfile'

    # Verify write succeeded
    if ! kappal exec app cat /data/testfile 2>/dev/null | grep -q "test-data"; then
        log_fail "$test_name - failed to write test data"
        kappal down -v
        return
    fi

    # Restart by doing down/up (kappal way, no kubectl exposure)
    kappal down
    kappal up -d

    # Wait for pod to be fully ready after restart
    sleep 15

    # Check data persists using kappal exec
    if kappal exec app cat /data/testfile 2>/dev/null | grep -q "test-data"; then
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

    # Check secret is mounted at /run/secrets/ using kappal exec
    if kappal exec app cat /run/secrets/my_secret 2>/dev/null | grep -q "secret-value"; then
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

    # Check config is mounted using kappal exec
    if kappal exec app cat /etc/app/config.json 2>/dev/null | grep -q "setting"; then
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

    # Verify UDP port is working by checking service is up
    # UDP protocol support is verified by successful deployment
    if kappal ps | grep -q "Up"; then
        # Internal verification: check K8s Service has UDP protocol
        # This is implementation validation, not user-facing
        if _internal_verify_udp_service "udp" "dns"; then
            log_pass "$test_name"
        else
            log_fail "$test_name - UDP port not configured correctly"
        fi
    else
        log_fail "$test_name - service not running"
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

    # Verify scaling by checking service is up
    if kappal ps | grep -q "Up"; then
        # Internal verification: check K8s Deployment has correct replicas
        # This is implementation validation, not user-facing
        replicas=$(_internal_get_replicas "scaling" "app")
        if [ "$replicas" = "3" ]; then
            log_pass "$test_name"
        else
            log_fail "$test_name - expected 3 replicas, got $replicas"
        fi
    else
        log_fail "$test_name - service not running"
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

    # Verify services are up
    if kappal ps | grep -q "Up"; then
        # Internal verification: check K8s NetworkPolicy was created
        # This is implementation validation, not user-facing
        if _internal_verify_networkpolicy "networks" "frontend-net"; then
            log_pass "$test_name"
        else
            log_fail "$test_name - NetworkPolicy not created"
        fi
    else
        log_fail "$test_name - services not running"
    fi

    kappal down -v
}

# Test: Jobs complete and don't restart (restart: "no" -> K8s Job)
test_job_lifecycle() {
    local test_name="JobLifecycle"
    local test_dir="$TESTDATA_DIR/jobs"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    # Wait for jobs to complete and service to start
    sleep 20

    # Assert 1: app service is running
    if ! kappal ps | grep -q "app.*Up"; then
        log_fail "$test_name - app service not running"
        kappal ps
        kappal down -v
        return
    fi

    # Assert 2: setup job completed (K8s Job, not Deployment)
    local setup_kind=$(_internal_get_resource_kind "jobs" "setup")
    if [ "$setup_kind" != "Job" ]; then
        log_fail "$test_name - 'setup' is $setup_kind, expected Job"
        kappal down -v
        return
    fi

    # Assert 3: setup pod reached Succeeded phase
    local setup_phase=$(_internal_get_pod_phase "jobs" "kappal.io/service=setup")
    if [ "$setup_phase" != "Succeeded" ]; then
        log_fail "$test_name - setup pod phase is '$setup_phase', expected Succeeded"
        kappal down -v
        return
    fi

    # Assert 4: Job pods have restartPolicy: Never
    local restart=$(_internal_get_restart_policy "jobs" "kappal.io/service=setup")
    if [ "$restart" != "Never" ]; then
        log_fail "$test_name - setup restart policy is '$restart', expected Never"
        kappal down -v
        return
    fi

    log_pass "$test_name"
    kappal down -v
}

# Test: depends_on service_completed_successfully ordering
test_dependency_ordering() {
    local test_name="DependencyOrdering"
    local test_dir="$TESTDATA_DIR/jobs"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 20

    # Assert 1: migrate job also completed
    local migrate_phase=$(_internal_get_pod_phase "jobs" "kappal.io/service=migrate")
    if [ "$migrate_phase" != "Succeeded" ]; then
        log_fail "$test_name - migrate pod phase is '$migrate_phase', expected Succeeded"
        kappal down -v
        return
    fi

    # Assert 2: app pod has init container (wait-for-deps) for dependency on migrate
    if ! _internal_has_init_container "jobs" "kappal.io/service=app" "wait-for-deps"; then
        log_fail "$test_name - app pod missing wait-for-deps init container"
        kappal down -v
        return
    fi

    # Assert 3: migrate pod has init container for dependency on setup
    if ! _internal_has_init_container "jobs" "kappal.io/service=migrate" "wait-for-deps"; then
        log_fail "$test_name - migrate pod missing wait-for-deps init container"
        kappal down -v
        return
    fi

    # Assert 4: app is Running (started after migrate completed)
    local app_phase=$(_internal_get_pod_phase "jobs" "kappal.io/service=app")
    if [ "$app_phase" != "Running" ]; then
        log_fail "$test_name - app pod phase is '$app_phase', expected Running"
        kappal down -v
        return
    fi

    log_pass "$test_name"
    kappal down -v
}

# Test: Profiles excluded from default up
test_profile_exclusion() {
    local test_name="ProfileExclusion"
    local test_dir="$TESTDATA_DIR/jobs"

    echo "Running test: $test_name"
    cd "$test_dir"

    if ! kappal up -d; then
        log_fail "$test_name - kappal up failed"
        return
    fi

    sleep 15

    # Assert: 'tools' service (profiles: [debug]) should NOT appear in ps
    if kappal ps 2>/dev/null | grep -q "tools"; then
        log_fail "$test_name - profiled service 'tools' should not be running"
        kappal down -v
        return
    fi

    # Internal: verify no K8s resources created for 'tools'
    if _internal_kubectl get deploy -n "jobs" "tools" -o name 2>/dev/null | grep -q "tools"; then
        log_fail "$test_name - Deployment created for profiled service 'tools'"
        kappal down -v
        return
    fi
    if _internal_kubectl get job -n "jobs" "tools" -o name 2>/dev/null | grep -q "tools"; then
        log_fail "$test_name - Job created for profiled service 'tools'"
        kappal down -v
        return
    fi

    log_pass "$test_name"
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

    # Run setup in each test directory
    # Setup pulls the K3s image (once) and creates .kappal/setup.json (per-project)
    echo "Running kappal setup in test directories..."
    for dir in "$TESTDATA_DIR"/*/; do
        if [ -d "$dir" ]; then
            (cd "$dir" && kappal --setup)
        fi
    done

    # Run tests
    test_simple_lifecycle
    test_simple_network
    test_volume_file
    test_secret_file
    test_config_file
    test_udp_port
    test_scaling
    test_different_networks
    test_job_lifecycle
    test_dependency_ordering
    test_profile_exclusion

    echo ""
    echo "=========================================="
    echo "Results: ${GREEN}$PASSED passed${NC}, ${RED}$FAILED failed${NC}, ${YELLOW}$SKIPPED skipped${NC}"
    echo "=========================================="

    if [ $FAILED -gt 0 ]; then
        exit 1
    fi
}

main "$@"
