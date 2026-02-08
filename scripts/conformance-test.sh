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
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

PASSED=0
FAILED=0
SKIPPED=0

# Timestamp helper
ts() {
    date '+%H:%M:%S'
}

log_info() {
    echo -e "[$(ts)] ${CYAN}INFO${NC}: $1"
}

log_pass() {
    echo -e "[$(ts)] ${GREEN}PASS${NC}: $1"
    ((PASSED++)) || true
}

log_fail() {
    echo -e "[$(ts)] ${RED}FAIL${NC}: $1"
    ((FAILED++)) || true
}

log_skip() {
    echo -e "[$(ts)] ${YELLOW}SKIP${NC}: $1"
    ((SKIPPED++)) || true
}

# Run a command with full output and timing
run_cmd() {
    local label="$1"
    shift
    echo -e "[$(ts)] ${BOLD}>>> $label:${NC} $*"
    local start_time=$(date +%s)
    "$@" 2>&1 | while IFS= read -r line; do
        echo -e "  [$(ts)]   $line"
    done
    local exit_code=${PIPESTATUS[0]}
    local end_time=$(date +%s)
    local elapsed=$((end_time - start_time))
    if [ $exit_code -ne 0 ]; then
        echo -e "[$(ts)] ${RED}^^^ COMMAND FAILED (exit=$exit_code, ${elapsed}s)${NC}"
    else
        echo -e "[$(ts)] ${GREEN}^^^ OK (${elapsed}s)${NC}"
    fi
    return $exit_code
}

# Dump all diagnostic info for debugging failures
dump_diagnostics() {
    local context="$1"
    echo ""
    echo -e "[$(ts)] ${YELLOW}===== DIAGNOSTICS: $context =====${NC}"

    echo -e "[$(ts)] ${BOLD}--- kappal ps ---${NC}"
    kappal ps 2>&1 || echo "  (kappal ps failed)"

    echo -e "[$(ts)] ${BOLD}--- kappal inspect ---${NC}"
    kappal inspect 2>&1 | head -100 || echo "  (kappal inspect failed)"

    echo -e "[$(ts)] ${BOLD}--- Docker containers (kappal-*) ---${NC}"
    docker ps -a --filter "name=kappal-" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" 2>&1

    echo -e "[$(ts)] ${BOLD}--- Docker networks (kappal-*) ---${NC}"
    docker network ls --filter "name=kappal-" 2>&1

    # Try to get K3s container logs (last 50 lines)
    local project_name
    project_name=$(basename "$PWD")
    local k3s_container="kappal-${project_name}-k3s"
    if docker ps -a --format '{{.Names}}' | grep -q "^${k3s_container}$"; then
        echo -e "[$(ts)] ${BOLD}--- K3s container logs (last 50 lines): $k3s_container ---${NC}"
        docker logs --tail 50 "$k3s_container" 2>&1 || echo "  (could not get K3s logs)"
    fi

    # Try kubectl diagnostics if kubeconfig exists
    local kubeconfig="$PWD/.kappal/runtime/kubeconfig.yaml"
    if [ -f "$kubeconfig" ]; then
        echo -e "[$(ts)] ${BOLD}--- kubectl get all -A ---${NC}"
        kubectl --kubeconfig="$kubeconfig" get all -A 2>&1 || echo "  (kubectl failed)"

        echo -e "[$(ts)] ${BOLD}--- kubectl get events -A --sort-by=.lastTimestamp ---${NC}"
        kubectl --kubeconfig="$kubeconfig" get events -A --sort-by='.lastTimestamp' 2>&1 | tail -30 || echo "  (kubectl events failed)"

        echo -e "[$(ts)] ${BOLD}--- kubectl describe pods -A ---${NC}"
        kubectl --kubeconfig="$kubeconfig" describe pods -A 2>&1 | tail -80 || echo "  (kubectl describe failed)"
    else
        echo -e "[$(ts)]   (no kubeconfig at $kubeconfig)"
    fi

    echo -e "[$(ts)] ${YELLOW}===== END DIAGNOSTICS =====${NC}"
    echo ""
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
        kubectl --kubeconfig="$kubeconfig" "$@"
    else
        # Fallback to docker exec if kubeconfig not available locally
        docker exec "kappal-$(basename "$PWD")-k3s" kubectl "$@"
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
    _internal_kubectl get pod -n "$namespace" -l "$label" -o jsonpath='{.items[0].status.phase}'
}

# Internal: Check if a pod has an init container with a given name
_internal_has_init_container() {
    local namespace="$1"
    local label="$2"
    local init_name="$3"
    _internal_kubectl get pod -n "$namespace" -l "$label" -o jsonpath='{.items[0].spec.initContainers[*].name}' | grep -q "$init_name"
}

# Internal: Get restart policy for a pod
_internal_get_restart_policy() {
    local namespace="$1"
    local label="$2"
    _internal_kubectl get pod -n "$namespace" -l "$label" -o jsonpath='{.items[0].spec.restartPolicy}'
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
            -e KAPPAL_HOST_DIR="$PWD" \
            --network host \
            kappal:latest -p "$(basename "$PWD")" "$@"
    fi
}

# Test: Simple lifecycle (up/down)
test_simple_lifecycle() {
    local test_name="SimpleLifecycle"
    local test_dir="$TESTDATA_DIR/simple"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    # Up
    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    log_info "Waiting 5s for services to settle..."
    sleep 5

    # Check status
    log_info "Checking service status..."
    local ps_output
    ps_output=$(kappal ps 2>&1)
    echo "$ps_output"
    if ! echo "$ps_output" | grep -q "running"; then
        log_fail "$test_name - service not running"
        dump_diagnostics "$test_name - service not running"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Down
    if ! run_cmd "kappal down" kappal down -v; then
        log_fail "$test_name - kappal down failed"
        dump_diagnostics "$test_name after down failure"
        return
    fi

    local test_end=$(date +%s)
    log_pass "$test_name ($(( test_end - test_start ))s)"
}

# Test: Service-to-service networking
test_simple_network() {
    local test_name="SimpleNetwork"
    local test_dir="$TESTDATA_DIR/network"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    # Wait for pods to be ready and DNS to stabilize
    log_info "Waiting 15s for pods + DNS..."
    sleep 15

    # Show state
    log_info "Current service state:"
    kappal ps 2>&1 || true

    # Look up the backend's container port dynamically via kappal inspect
    log_info "Getting backend port from kappal inspect..."
    local inspect_output
    inspect_output=$(kappal inspect 2>&1)
    echo "$inspect_output" | head -30
    BACKEND_PORT=$(echo "$inspect_output" | jq -r '.services[] | select(.name=="backend") | .ports[0].container')
    log_info "Backend port: $BACKEND_PORT"
    if [ -z "$BACKEND_PORT" ] || [ "$BACKEND_PORT" = "null" ]; then
        log_fail "$test_name - could not determine backend port from kappal inspect"
        dump_diagnostics "$test_name - no backend port"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Test that frontend can reach backend by service name
    log_info "Testing frontend -> backend connectivity on port $BACKEND_PORT..."
    local exec_output
    exec_output=$(kappal exec frontend sh -c "echo 'GET /' | nc -w 5 backend $BACKEND_PORT" 2>&1) || true
    echo "  exec output: $exec_output"
    if echo "$exec_output" | grep -q "OK"; then
        local test_end=$(date +%s)
        log_pass "$test_name ($(( test_end - test_start ))s)"
    else
        log_fail "$test_name - service-to-service communication failed"
        dump_diagnostics "$test_name - connectivity failed"
    fi

    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Test: Volume persistence
test_volume_file() {
    local test_name="VolumeFile"
    local test_dir="$TESTDATA_DIR/volume"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    # Wait for pod to be fully ready
    log_info "Waiting 10s for pod to be ready..."
    sleep 10

    log_info "Current service state:"
    kappal ps 2>&1 || true

    # Write data to volume using kappal exec
    log_info "Writing test data to volume..."
    run_cmd "kappal exec (write)" kappal exec app sh -c 'echo "test-data" > /data/testfile'

    # Verify write succeeded
    log_info "Verifying write..."
    local read_output
    read_output=$(kappal exec app cat /data/testfile 2>&1) || true
    echo "  read output: $read_output"
    if ! echo "$read_output" | grep -q "test-data"; then
        log_fail "$test_name - failed to write test data"
        dump_diagnostics "$test_name - write failed"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Restart by doing down/up (kappal way, no kubectl exposure)
    log_info "Restarting: down then up..."
    run_cmd "kappal down" kappal down
    run_cmd "kappal up (2nd)" kappal up -d

    # Wait for pod to be fully ready after restart
    log_info "Waiting 15s for pod to be ready after restart..."
    sleep 15

    log_info "Current service state after restart:"
    kappal ps 2>&1 || true

    # Check data persists using kappal exec
    log_info "Checking volume persistence..."
    local persist_output
    persist_output=$(kappal exec app cat /data/testfile 2>&1) || true
    echo "  persist output: $persist_output"
    if echo "$persist_output" | grep -q "test-data"; then
        local test_end=$(date +%s)
        log_pass "$test_name ($(( test_end - test_start ))s)"
    else
        log_fail "$test_name - volume data not persisted"
        dump_diagnostics "$test_name - persistence failed"
    fi

    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Test: Secrets
test_secret_file() {
    local test_name="SecretFile"
    local test_dir="$TESTDATA_DIR/secret"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    log_info "Waiting 5s for services..."
    sleep 5

    log_info "Current service state:"
    kappal ps 2>&1 || true

    # Check secret is mounted at /run/secrets/ using kappal exec
    log_info "Checking secret at /run/secrets/my_secret..."
    local secret_output
    secret_output=$(kappal exec app cat /run/secrets/my_secret 2>&1) || true
    echo "  secret output: $secret_output"
    if echo "$secret_output" | grep -q "secret-value"; then
        local test_end=$(date +%s)
        log_pass "$test_name ($(( test_end - test_start ))s)"
    else
        log_fail "$test_name - secret not accessible"
        dump_diagnostics "$test_name - secret not found"
    fi

    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Test: Config files
test_config_file() {
    local test_name="ConfigFile"
    local test_dir="$TESTDATA_DIR/config"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    log_info "Waiting 5s for services..."
    sleep 5

    log_info "Current service state:"
    kappal ps 2>&1 || true

    # Check config is mounted using kappal exec
    log_info "Checking config at /etc/app/config.json..."
    local config_output
    config_output=$(kappal exec app cat /etc/app/config.json 2>&1) || true
    echo "  config output: $config_output"
    if echo "$config_output" | grep -q "setting"; then
        local test_end=$(date +%s)
        log_pass "$test_name ($(( test_end - test_start ))s)"
    else
        log_fail "$test_name - config not accessible"
        dump_diagnostics "$test_name - config not found"
    fi

    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Test: UDP ports
test_udp_port() {
    local test_name="UdpPort"
    local test_dir="$TESTDATA_DIR/udp"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    log_info "Waiting 5s for services..."
    sleep 5

    log_info "Current service state:"
    local ps_output
    ps_output=$(kappal ps 2>&1) || true
    echo "$ps_output"

    # Verify UDP port is working by checking service is up
    if echo "$ps_output" | grep -q "running"; then
        # Internal verification: check K8s Service has UDP protocol
        log_info "Verifying K8s Service has UDP protocol..."
        if _internal_verify_udp_service "udp" "dns"; then
            local test_end=$(date +%s)
            log_pass "$test_name ($(( test_end - test_start ))s)"
        else
            log_fail "$test_name - UDP port not configured correctly"
            dump_diagnostics "$test_name - UDP misconfigured"
        fi
    else
        log_fail "$test_name - service not running"
        dump_diagnostics "$test_name - not running"
    fi

    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Test: Scaling with replicas
test_scaling() {
    local test_name="Scaling"
    local test_dir="$TESTDATA_DIR/scaling"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    log_info "Waiting 10s for replicas..."
    sleep 10

    log_info "Current service state:"
    local ps_output
    ps_output=$(kappal ps 2>&1) || true
    echo "$ps_output"

    # Verify scaling by checking service is up
    if echo "$ps_output" | grep -q "running"; then
        # Internal verification: check K8s Deployment has correct replicas
        local replicas
        replicas=$(_internal_get_replicas "scaling" "app")
        log_info "Replica count: $replicas (expected 3)"
        if [ "$replicas" = "3" ]; then
            local test_end=$(date +%s)
            log_pass "$test_name ($(( test_end - test_start ))s)"
        else
            log_fail "$test_name - expected 3 replicas, got $replicas"
            dump_diagnostics "$test_name - wrong replica count"
        fi
    else
        log_fail "$test_name - service not running"
        dump_diagnostics "$test_name - not running"
    fi

    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Test: Network isolation
test_different_networks() {
    local test_name="DifferentNetworks"
    local test_dir="$TESTDATA_DIR/networks"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    log_info "Waiting 10s for services..."
    sleep 10

    log_info "Current service state:"
    local ps_output
    ps_output=$(kappal ps 2>&1) || true
    echo "$ps_output"

    # Verify services are up
    if echo "$ps_output" | grep -q "running"; then
        # Internal verification: check K8s NetworkPolicy was created
        log_info "Checking NetworkPolicy..."
        if _internal_verify_networkpolicy "networks" "frontend-net"; then
            local test_end=$(date +%s)
            log_pass "$test_name ($(( test_end - test_start ))s)"
        else
            log_fail "$test_name - NetworkPolicy not created"
            dump_diagnostics "$test_name - no NetworkPolicy"
        fi
    else
        log_fail "$test_name - services not running"
        dump_diagnostics "$test_name - not running"
    fi

    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Test: Jobs complete and don't restart (restart: "no" -> K8s Job)
test_job_lifecycle() {
    local test_name="JobLifecycle"
    local test_dir="$TESTDATA_DIR/jobs"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    # Wait for jobs to complete and service to start
    log_info "Waiting 20s for jobs to complete..."
    sleep 20

    log_info "Current service state:"
    local ps_output
    ps_output=$(kappal ps 2>&1) || true
    echo "$ps_output"

    # Assert 1: app service is running
    if ! echo "$ps_output" | grep -q "app.*running"; then
        log_fail "$test_name - app service not running"
        dump_diagnostics "$test_name - app not running"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Assert 2: setup job completed (K8s Job, not Deployment)
    local setup_kind
    setup_kind=$(_internal_get_resource_kind "jobs" "setup")
    log_info "setup resource kind: $setup_kind (expected Job)"
    if [ "$setup_kind" != "Job" ]; then
        log_fail "$test_name - 'setup' is $setup_kind, expected Job"
        dump_diagnostics "$test_name - wrong resource kind"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Assert 3: setup pod reached Succeeded phase
    local setup_phase
    setup_phase=$(_internal_get_pod_phase "jobs" "kappal.io/service=setup")
    log_info "setup pod phase: $setup_phase (expected Succeeded)"
    if [ "$setup_phase" != "Succeeded" ]; then
        log_fail "$test_name - setup pod phase is '$setup_phase', expected Succeeded"
        dump_diagnostics "$test_name - wrong setup phase"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Assert 4: Job pods have restartPolicy: Never
    local restart
    restart=$(_internal_get_restart_policy "jobs" "kappal.io/service=setup")
    log_info "setup restart policy: $restart (expected Never)"
    if [ "$restart" != "Never" ]; then
        log_fail "$test_name - setup restart policy is '$restart', expected Never"
        dump_diagnostics "$test_name - wrong restart policy"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    local test_end=$(date +%s)
    log_pass "$test_name ($(( test_end - test_start ))s)"
    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Test: depends_on service_completed_successfully ordering
test_dependency_ordering() {
    local test_name="DependencyOrdering"
    local test_dir="$TESTDATA_DIR/jobs"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    log_info "Waiting 20s for jobs and dependencies..."
    sleep 20

    log_info "Current service state:"
    kappal ps 2>&1 || true

    # Assert 1: migrate job also completed
    local migrate_phase
    migrate_phase=$(_internal_get_pod_phase "jobs" "kappal.io/service=migrate")
    log_info "migrate pod phase: $migrate_phase (expected Succeeded)"
    if [ "$migrate_phase" != "Succeeded" ]; then
        log_fail "$test_name - migrate pod phase is '$migrate_phase', expected Succeeded"
        dump_diagnostics "$test_name - wrong migrate phase"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Assert 2: app pod has init container (wait-for-deps) for dependency on migrate
    log_info "Checking app pod for wait-for-deps init container..."
    if ! _internal_has_init_container "jobs" "kappal.io/service=app" "wait-for-deps"; then
        log_fail "$test_name - app pod missing wait-for-deps init container"
        log_info "Init containers found:"
        _internal_kubectl get pod -n "jobs" -l "kappal.io/service=app" -o jsonpath='{.items[0].spec.initContainers[*].name}' || true
        echo ""
        dump_diagnostics "$test_name - missing init container"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Assert 3: migrate pod has init container for dependency on setup
    log_info "Checking migrate pod for wait-for-deps init container..."
    if ! _internal_has_init_container "jobs" "kappal.io/service=migrate" "wait-for-deps"; then
        log_fail "$test_name - migrate pod missing wait-for-deps init container"
        log_info "Init containers found:"
        _internal_kubectl get pod -n "jobs" -l "kappal.io/service=migrate" -o jsonpath='{.items[0].spec.initContainers[*].name}' || true
        echo ""
        dump_diagnostics "$test_name - missing init container"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Assert 4: app is Running (started after migrate completed)
    local app_phase
    app_phase=$(_internal_get_pod_phase "jobs" "kappal.io/service=app")
    log_info "app pod phase: $app_phase (expected Running)"
    if [ "$app_phase" != "Running" ]; then
        log_fail "$test_name - app pod phase is '$app_phase', expected Running"
        dump_diagnostics "$test_name - wrong app phase"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    local test_end=$(date +%s)
    log_pass "$test_name ($(( test_end - test_start ))s)"
    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Test: Profiles excluded from default up
test_profile_exclusion() {
    local test_name="ProfileExclusion"
    local test_dir="$TESTDATA_DIR/jobs"
    local test_start=$(date +%s)

    echo ""
    echo -e "[$(ts)] ${BOLD}========== TEST: $test_name ==========${NC}"
    cd "$test_dir"
    log_info "Working directory: $test_dir"

    if ! run_cmd "kappal up" kappal up -d; then
        log_fail "$test_name - kappal up failed"
        dump_diagnostics "$test_name after up failure"
        return
    fi

    log_info "Waiting 15s for services..."
    sleep 15

    log_info "Current service state:"
    local ps_output
    ps_output=$(kappal ps 2>&1) || true
    echo "$ps_output"

    # Assert: 'tools' service (profiles: [debug]) should NOT appear in ps
    if echo "$ps_output" | grep -q "tools"; then
        log_fail "$test_name - profiled service 'tools' should not be running"
        dump_diagnostics "$test_name - profiled service running"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    # Internal: verify no K8s resources created for 'tools'
    log_info "Verifying no K8s resources for 'tools'..."
    if _internal_kubectl get deploy -n "jobs" "tools" -o name 2>/dev/null | grep -q "tools"; then
        log_fail "$test_name - Deployment created for profiled service 'tools'"
        dump_diagnostics "$test_name - unexpected deployment"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi
    if _internal_kubectl get job -n "jobs" "tools" -o name 2>/dev/null | grep -q "tools"; then
        log_fail "$test_name - Job created for profiled service 'tools'"
        dump_diagnostics "$test_name - unexpected job"
        run_cmd "kappal down (cleanup)" kappal down -v || true
        return
    fi

    local test_end=$(date +%s)
    log_pass "$test_name ($(( test_end - test_start ))s)"
    run_cmd "kappal down (cleanup)" kappal down -v || true
}

# Main
main() {
    echo "=========================================="
    echo "Kappal Conformance Tests"
    echo "Started at: $(date)"
    echo "=========================================="

    check_docker
    cleanup

    trap cleanup EXIT

    # Show Docker state before tests
    log_info "Docker containers before tests:"
    docker ps -a --filter "name=kappal-" --format "table {{.Names}}\t{{.Status}}" 2>&1 || true

    # Run setup in each test directory
    echo ""
    log_info "Running kappal setup in test directories..."
    for dir in "$TESTDATA_DIR"/*/; do
        if [ -d "$dir" ]; then
            log_info "Setup: $(basename "$dir")"
            (cd "$dir" && kappal --setup) 2>&1
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
    echo -e "Results: ${GREEN}$PASSED passed${NC}, ${RED}$FAILED failed${NC}, ${YELLOW}$SKIPPED skipped${NC}"
    echo "Finished at: $(date)"
    echo "=========================================="

    if [ $FAILED -gt 0 ]; then
        exit 1
    fi
}

main "$@"
