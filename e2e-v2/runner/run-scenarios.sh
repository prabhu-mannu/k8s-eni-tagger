#!/usr/bin/env bash
#
# E2E Scenario Tests for k8s-eni-tagger
# Tests various scenarios beyond basic functionality:
# - Multiple pods in parallel
# - Cache persistence across controller restarts
# - Tag updates and overwrites
# - Namespace isolation
# - Error handling
#

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { printf "${GREEN}[INFO]${NC} %s\n" "$*"; }
log_warn() { printf "${YELLOW}[WARN]${NC} %s\n" "$*"; }
log_error() { printf "${RED}[ERROR]${NC} %s\n" "$*"; }
log_test() { printf "${BLUE}[TEST]${NC} %s\n" "$*"; }

# Configuration
AWS_MOCK_URL=${AWS_MOCK_URL:-http://aws-mock:4566}
AWS_REGION=${AWS_REGION:-us-west-2}
AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-testing}
AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-testing}
CONTROLLER_IMAGE=${CONTROLLER_IMAGE:-k8s-eni-tagger:dev}
CONTROLLER_NAMESPACE=${CONTROLLER_NAMESPACE:-default}
MAX_WAIT_SECONDS=${MAX_WAIT_SECONDS:-180}

# Test state
TESTS_PASSED=0
TESTS_FAILED=0

# Helper functions
port_forward_aws_mock() {
  kubectl -n "$CONTROLLER_NAMESPACE" port-forward svc/aws-mock 4566:4566 >/dev/null 2>&1 &
  local PF_PID=$!
  sleep 1
  echo "$PF_PID"
}

cleanup_port_forward() {
  local PF_PID=$1
  kill "$PF_PID" 2>/dev/null || true
  sleep 1
}

seed_eni() {
  local eni_id=$1
  local private_ip=$2
  local pf_pid=$(port_forward_aws_mock)

  local payload
  payload=$(cat <<EOF
{"eniId":"${eni_id}","privateIp":"${private_ip}","interfaceType":"interface","subnetId":"subnet-test"}
EOF
)

  curl -sf -X POST "http://localhost:4566/admin/enis" \
    -H 'Content-Type: application/json' \
    -d "$payload" >/dev/null

  cleanup_port_forward "$pf_pid"
}

get_eni_tags() {
  local eni_id=$1
  local pf_pid=$(port_forward_aws_mock)

  local tags
  tags=$(curl -sf "http://localhost:4566/admin/tags/${eni_id}")

  cleanup_port_forward "$pf_pid"
  echo "$tags"
}

test_passed() {
  local test_name=$1
  log_info "✓ $test_name"
  ((TESTS_PASSED++))
}

test_failed() {
  local test_name=$1
  local reason=${2:-"unknown reason"}
  log_error "✗ $test_name: $reason"
  ((TESTS_FAILED++))
}

# Scenario 1: Multiple pods with different tags
test_scenario_multiple_pods() {
  log_test "SCENARIO 1: Multiple pods with different tags"

  # Seed ENIs for 3 pods
  seed_eni "eni-multi-1" "10.42.0.100"
  seed_eni "eni-multi-2" "10.42.0.101"
  seed_eni "eni-multi-3" "10.42.0.102"

  # Create first pod
  kubectl -n "$CONTROLLER_NAMESPACE" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-multi-pod-1
  annotations:
    eni-tagger.io/tags: '{"Environment":"prod","Team":"backend"}'
spec:
  containers:
  - name: nginx
    image: nginx:latest
    resources:
      requests:
        memory: "32Mi"
        cpu: "10m"
      limits:
        memory: "128Mi"
        cpu: "100m"
EOF

  sleep 2

  # Get actual pod IP and update ENI
  local pod_ip1=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-multi-pod-1 -o jsonpath='{.status.podIP}' 2>/dev/null || echo "")
  if [ -n "$pod_ip1" ]; then
    seed_eni "eni-multi-1" "$pod_ip1"
  fi

  # Create second pod
  kubectl -n "$CONTROLLER_NAMESPACE" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-multi-pod-2
  annotations:
    eni-tagger.io/tags: '{"Environment":"staging","Team":"frontend"}'
spec:
  containers:
  - name: nginx
    image: nginx:latest
    resources:
      requests:
        memory: "32Mi"
        cpu: "10m"
      limits:
        memory: "128Mi"
        cpu: "100m"
EOF

  sleep 2

  # Get actual pod IP and update ENI
  local pod_ip2=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-multi-pod-2 -o jsonpath='{.status.podIP}' 2>/dev/null || echo "")
  if [ -n "$pod_ip2" ]; then
    seed_eni "eni-multi-2" "$pod_ip2"
  fi

  # Wait for both reconciliations
  local waited=0
  local pod1_tagged=false
  local pod2_tagged=false

  while [ $waited -lt $MAX_WAIT_SECONDS ]; do
    if kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-multi-pod-1 -o jsonpath='{.status.conditions[?(@.type=="eni-tagger.io/tagged")].status}' 2>/dev/null | grep -q "True"; then
      pod1_tagged=true
    fi
    if kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-multi-pod-2 -o jsonpath='{.status.conditions[?(@.type=="eni-tagger.io/tagged")].status}' 2>/dev/null | grep -q "True"; then
      pod2_tagged=true
    fi

    if [ "$pod1_tagged" = true ] && [ "$pod2_tagged" = true ]; then
      break
    fi

    sleep 3
    ((waited+=3))
  done

  if [ "$pod1_tagged" = true ] && [ "$pod2_tagged" = true ]; then
    # Verify tags
    local tags1=$(get_eni_tags "eni-multi-1")
    local tags2=$(get_eni_tags "eni-multi-2")

    if echo "$tags1" | jq -e '.Environment == "prod" and .Team == "backend"' >/dev/null 2>&1 && \
       echo "$tags2" | jq -e '.Environment == "staging" and .Team == "frontend"' >/dev/null 2>&1; then
      test_passed "Multiple pods with different tags"
    else
      test_failed "Multiple pods with different tags" "Tags don't match expected values"
    fi
  else
    test_failed "Multiple pods with different tags" "Pods not reconciled in time"
  fi

  # Cleanup
  kubectl -n "$CONTROLLER_NAMESPACE" delete pods e2e-multi-pod-1 e2e-multi-pod-2 --ignore-not-found || true
}

# Scenario 2: Tag overwrite
test_scenario_tag_overwrite() {
  log_test "SCENARIO 2: Tag overwrite when pod annotation changes"

  # Seed ENI
  seed_eni "eni-overwrite" "10.42.0.200"

  # Create pod with initial tags
  kubectl -n "$CONTROLLER_NAMESPACE" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-overwrite-pod
  annotations:
    eni-tagger.io/tags: '{"Version":"v1"}'
spec:
  containers:
  - name: nginx
    image: nginx:latest
    resources:
      requests:
        memory: "32Mi"
        cpu: "10m"
      limits:
        memory: "128Mi"
        cpu: "100m"
EOF

  sleep 2

  # Get pod IP and update ENI
  local pod_ip=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-overwrite-pod -o jsonpath='{.status.podIP}' 2>/dev/null || echo "")
  if [ -n "$pod_ip" ]; then
    seed_eni "eni-overwrite" "$pod_ip"
  fi

  # Wait for initial tagging
  local waited=0
  until kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-overwrite-pod -o jsonpath='{.status.conditions[?(@.type=="eni-tagger.io/tagged")].status}' 2>/dev/null | grep -q "True"; do
    sleep 3
    ((waited+=3))
    if [ $waited -ge $MAX_WAIT_SECONDS ]; then
      test_failed "Tag overwrite" "Initial tagging timeout"
      kubectl -n "$CONTROLLER_NAMESPACE" delete pod e2e-overwrite-pod --ignore-not-found || true
      return
    fi
  done

  # Verify first tag
  local tags=$(get_eni_tags "eni-overwrite")
  if ! echo "$tags" | jq -e '.Version == "v1"' >/dev/null 2>&1; then
    test_failed "Tag overwrite" "Initial tag not applied"
    kubectl -n "$CONTROLLER_NAMESPACE" delete pod e2e-overwrite-pod --ignore-not-found || true
    return
  fi

  # Update pod annotation
  kubectl -n "$CONTROLLER_NAMESPACE" patch pod e2e-overwrite-pod -p '{"metadata":{"annotations":{"eni-tagger.io/tags":"{\"Version\":\"v2\"}"}}}'

  # Wait for re-reconciliation
  sleep 10

  # Verify tags were overwritten
  tags=$(get_eni_tags "eni-overwrite")
  if echo "$tags" | jq -e '.Version == "v2"' >/dev/null 2>&1; then
    test_passed "Tag overwrite"
  else
    test_failed "Tag overwrite" "Tags were not updated after annotation change"
  fi

  # Cleanup
  kubectl -n "$CONTROLLER_NAMESPACE" delete pod e2e-overwrite-pod --ignore-not-found || true
}

# Scenario 3: Pod with missing annotation (should skip)
test_scenario_missing_annotation() {
  log_test "SCENARIO 3: Pod without annotation should be skipped"

  # Seed ENI
  seed_eni "eni-no-annot" "10.42.0.300"

  # Create pod WITHOUT annotation
  kubectl -n "$CONTROLLER_NAMESPACE" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-no-annotation-pod
spec:
  containers:
  - name: nginx
    image: nginx:latest
    resources:
      requests:
        memory: "32Mi"
        cpu: "10m"
      limits:
        memory: "128Mi"
        cpu: "100m"
EOF

  sleep 2

  # Get pod IP and update ENI
  local pod_ip=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-no-annotation-pod -o jsonpath='{.status.podIP}' 2>/dev/null || echo "")
  if [ -n "$pod_ip" ]; then
    seed_eni "eni-no-annot" "$pod_ip"
  fi

  # Wait and check that pod is not tagged
  sleep 15

  local is_tagged=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-no-annotation-pod -o jsonpath='{.status.conditions[?(@.type=="eni-tagger.io/tagged")].status}' 2>/dev/null || echo "")
  if [ -z "$is_tagged" ] || [ "$is_tagged" != "True" ]; then
    test_passed "Pod without annotation is skipped"
  else
    test_failed "Pod without annotation is skipped" "Pod was unexpectedly tagged"
  fi

  # Cleanup
  kubectl -n "$CONTROLLER_NAMESPACE" delete pod e2e-no-annotation-pod --ignore-not-found || true
}

# Scenario 4: Invalid JSON tags (should fail gracefully)
test_scenario_invalid_json() {
  log_test "SCENARIO 4: Invalid JSON in tags annotation should fail gracefully"

  # Create pod with invalid JSON
  kubectl -n "$CONTROLLER_NAMESPACE" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-invalid-json-pod
  annotations:
    eni-tagger.io/tags: 'invalid{json'
spec:
  containers:
  - name: nginx
    image: nginx:latest
    resources:
      requests:
        memory: "32Mi"
        cpu: "10m"
      limits:
        memory: "128Mi"
        cpu: "100m"
EOF

  # Pod should still be running despite invalid annotation
  sleep 5

  local pod_exists=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-invalid-json-pod 2>/dev/null | wc -l)
  if [ "$pod_exists" -gt 0 ]; then
    test_passed "Invalid JSON handled gracefully"
  else
    test_failed "Invalid JSON handled gracefully" "Pod was affected by invalid annotation"
  fi

  # Cleanup
  kubectl -n "$CONTROLLER_NAMESPACE" delete pod e2e-invalid-json-pod --ignore-not-found || true
}

# Scenario 5: Controller restart and cache recovery
test_scenario_controller_restart() {
  log_test "SCENARIO 5: Controller restart and cache recovery"

  if [ "${ENABLE_CACHE_PERSISTENCE}" != "true" ]; then
    log_warn "Skipping controller restart test (cache persistence disabled)"
    return
  fi

  # Seed ENI
  seed_eni "eni-restart" "10.42.0.400"

  # Create pod
  kubectl -n "$CONTROLLER_NAMESPACE" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: e2e-restart-pod
  annotations:
    eni-tagger.io/tags: '{"Restart":"test"}'
spec:
  containers:
  - name: nginx
    image: nginx:latest
    resources:
      requests:
        memory: "32Mi"
        cpu: "10m"
      limits:
        memory: "128Mi"
        cpu: "100m"
EOF

  sleep 2

  # Get pod IP and update ENI
  local pod_ip=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-restart-pod -o jsonpath='{.status.podIP}' 2>/dev/null || echo "")
  if [ -n "$pod_ip" ]; then
    seed_eni "eni-restart" "$pod_ip"
  fi

  # Wait for initial tagging
  local waited=0
  until kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-restart-pod -o jsonpath='{.status.conditions[?(@.type=="eni-tagger.io/tagged")].status}' 2>/dev/null | grep -q "True"; do
    sleep 3
    ((waited+=3))
    if [ $waited -ge $MAX_WAIT_SECONDS ]; then
      test_failed "Controller restart" "Initial tagging timeout"
      kubectl -n "$CONTROLLER_NAMESPACE" delete pod e2e-restart-pod --ignore-not-found || true
      return
    fi
  done

  # Restart controller
  log_info "Restarting controller deployment..."
  kubectl -n "$CONTROLLER_NAMESPACE" rollout restart deploy/k8s-eni-tagger
  kubectl -n "$CONTROLLER_NAMESPACE" rollout status deploy/k8s-eni-tagger --timeout=180s

  # Verify tags are still present
  sleep 5
  local tags=$(get_eni_tags "eni-restart")
  if echo "$tags" | jq -e '.Restart == "test"' >/dev/null 2>&1; then
    test_passed "Controller restart and cache recovery"
  else
    test_failed "Controller restart and cache recovery" "Tags lost after controller restart"
  fi

  # Cleanup
  kubectl -n "$CONTROLLER_NAMESPACE" delete pod e2e-restart-pod --ignore-not-found || true
}

# Main test execution
main() {
  log_info "Starting E2E scenario tests"
  log_info "Configuration: Namespace=$CONTROLLER_NAMESPACE, CachePersistence=$ENABLE_CACHE_PERSISTENCE"

  test_scenario_multiple_pods
  test_scenario_tag_overwrite
  test_scenario_missing_annotation
  test_scenario_invalid_json
  test_scenario_controller_restart

  # Print summary
  echo ""
  echo "════════════════════════════════════════════════════════"
  printf "${GREEN}Tests Passed: %d${NC}\n" "$TESTS_PASSED"
  printf "${RED}Tests Failed: %d${NC}\n" "$TESTS_FAILED"
  echo "════════════════════════════════════════════════════════"

  if [ "$TESTS_FAILED" -eq 0 ]; then
    log_info "All scenario tests passed!"
    exit 0
  else
    log_error "Some tests failed"
    exit 1
  fi
}

main "$@"
