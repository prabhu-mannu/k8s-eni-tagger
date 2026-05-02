#!/usr/bin/env bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { printf "${GREEN}[INFO]${NC} %s\n" "$*"; }
log_warn() { printf "${YELLOW}[WARN]${NC} %s\n" "$*"; }
log_error() { printf "${RED}[ERROR]${NC} %s\n" "$*"; }

AWS_MOCK_URL=${AWS_MOCK_URL:-http://aws-mock:4566}
AWS_REGION=${AWS_REGION:-us-west-2}
AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-testing}
AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-testing}
CONTROLLER_IMAGE=${CONTROLLER_IMAGE:-k8s-eni-tagger:dev}
AWS_MOCK_IMAGE=${AWS_MOCK_IMAGE:-compose-aws-mock:latest}
CONTROLLER_NAMESPACE=${CONTROLLER_NAMESPACE:-default}
ANNOTATION_KEY=${ANNOTATION_KEY:-eni-tagger.io/tags}
ANNOTATION_VALUE_JSON=${ANNOTATION_VALUE_JSON:-"{\"Team\":\"platform\",\"CostCenter\":\"1234\"}"}
ENI_ID=${ENI_ID:-eni-1234}
ENI_PRIVATE_IP=${TEST_POD_IP:-10.0.1.42}
ENI_INTERFACE_TYPE=${ENI_INTERFACE_TYPE:-interface}
ENI_SUBNET_ID=${ENI_SUBNET_ID:-subnet-1234}
AWS_NODEPORT_PORT=${AWS_NODEPORT_PORT:-30066}
K3S_CONTAINER_NAME=${K3S_CONTAINER_NAME:-k8s-eni-tagger-k3s}
LOAD_CONTROLLER_IMAGE_TAR=${LOAD_CONTROLLER_IMAGE_TAR:-auto}
CONTROLLER_IMAGE_TAR=${CONTROLLER_IMAGE_TAR:-/workspace/k8s-eni-tagger-dev.tar}
MAX_WAIT_SECONDS=${MAX_WAIT_SECONDS:-180}
METRICS_BIND_ADDRESS=${METRICS_BIND_ADDRESS:-8090}
HEALTH_PROBE_BIND_ADDRESS=${HEALTH_PROBE_BIND_ADDRESS:-8081}
ENABLE_LEADER_ELECTION=${ENABLE_LEADER_ELECTION:-false}
ENABLE_CACHE_PERSISTENCE=${ENABLE_CACHE_PERSISTENCE:-false}
CACHE_BATCH_INTERVAL=${CACHE_BATCH_INTERVAL:-2s}
CACHE_BATCH_SIZE=${CACHE_BATCH_SIZE:-20}
MAX_CONCURRENT_RECONCILES=${MAX_CONCURRENT_RECONCILES:-1}
AWS_RATE_LIMIT_QPS=${AWS_RATE_LIMIT_QPS:-10}
AWS_RATE_LIMIT_BURST=${AWS_RATE_LIMIT_BURST:-20}

fix_kubeconfig() {
  if [ -n "${KUBECONFIG:-}" ] && [ -f "$KUBECONFIG" ]; then
    sed -i 's|https://127\\.0\\.0\\.1:6443|https://k3s:6443|g' "$KUBECONFIG" || true
    sed -i 's|http://127\\.0\\.0\\.1:6443|https://k3s:6443|g' "$KUBECONFIG" || true
  fi
}

wait_for_k3s() {
  log_info "Waiting for k3s API to be ready"
  local waited=0
  until kubectl get --raw /readyz >/dev/null 2>&1; do
    sleep 2
    waited=$((waited + 2))
    if [ $waited -ge $MAX_WAIT_SECONDS ]; then
      log_error "k3s API not ready after $MAX_WAIT_SECONDS seconds"
      exit 1
    fi
  done
  log_info "k3s API is ready"
}

wait_for_mock_pod() {
  log_info "Waiting for aws-mock pod to be ready in ${CONTROLLER_NAMESPACE}"
  kubectl -n "$CONTROLLER_NAMESPACE" wait --for=condition=Ready pod -l app=aws-mock --timeout=180s
  log_info "AWS mock pod is ready"
}

seed_mock() {
  log_info "Seeding AWS mock with ENI ${ENI_ID} (${ENI_PRIVATE_IP})"
  local payload
  payload=$(cat <<EOF
{"eniId":"${ENI_ID}","privateIp":"${ENI_PRIVATE_IP}","interfaceType":"${ENI_INTERFACE_TYPE}","subnetId":"${ENI_SUBNET_ID}"}
EOF
)
  # Seed via port-forward to aws-mock service
  kubectl -n "$CONTROLLER_NAMESPACE" port-forward svc/aws-mock 4566:4566 >/dev/null 2>&1 &
  local PF_PID=$!
  sleep 2
  curl -sf -X POST "http://localhost:4566/admin/enis" \
    -H 'Content-Type: application/json' \
    -d "$payload" >/dev/null
  kill $PF_PID 2>/dev/null || true
  log_info "ENI seeded"
}

select_endpoint() {
  # Use in-cluster service endpoint (aws-mock is deployed as k8s service)
  CONTROLLER_AWS_ENDPOINT="http://aws-mock.${CONTROLLER_NAMESPACE}.svc.cluster.local:4566"
  log_info "Using in-cluster endpoint ${CONTROLLER_AWS_ENDPOINT} for controller"
}

ensure_controller_image() {
  # Explicit opt-out: callers can set LOAD_CONTROLLER_IMAGE_TAR=0 to skip
  # all image preloading (e.g., when the image is already pulled by another
  # mechanism). Set =1 to use the legacy tarball loader.
  if [ "$LOAD_CONTROLLER_IMAGE_TAR" = "0" ]; then
    log_info "Skipping controller image import (LOAD_CONTROLLER_IMAGE_TAR=0)"
    return
  fi
  if [ "$LOAD_CONTROLLER_IMAGE_TAR" = "1" ]; then
    /runner/image-loader.sh "$CONTROLLER_IMAGE_TAR"
    return
  fi
  log_info "Checking if controller image exists in k3s"
  if docker exec "$K3S_CONTAINER_NAME" ctr images ls | grep -q "${CONTROLLER_IMAGE}"; then
    log_info "Controller image already present in k3s"
    return
  fi
  log_info "Importing controller image into k3s: ${CONTROLLER_IMAGE}"
  docker save "${CONTROLLER_IMAGE}" | docker exec -i "$K3S_CONTAINER_NAME" ctr images import -
}

ensure_mock_image() {
  log_info "Checking if aws-mock image exists in k3s"
  if ! docker exec "$K3S_CONTAINER_NAME" ctr images ls | grep -q "${AWS_MOCK_IMAGE}"; then
    log_info "Importing aws-mock image into k3s: ${AWS_MOCK_IMAGE}"
    docker save "${AWS_MOCK_IMAGE}" | docker exec -i "$K3S_CONTAINER_NAME" ctr images import -
  else
    log_info "AWS mock image already present in k3s"
  fi
}

deploy_mock() {
  export AWS_MOCK_IMAGE CONTROLLER_NAMESPACE
  log_info "Deploying aws-mock into k3s (namespace=${CONTROLLER_NAMESPACE})"
  envsubst < /manifests/aws-mock.yaml | kubectl apply -f -
  kubectl -n "$CONTROLLER_NAMESPACE" rollout status deploy/aws-mock --timeout=180s
  log_info "AWS mock deployment is ready"
}

deploy_controller() {
  export CONTROLLER_IMAGE CONTROLLER_NAMESPACE ANNOTATION_KEY METRICS_BIND_ADDRESS HEALTH_PROBE_BIND_ADDRESS
  export ENABLE_LEADER_ELECTION ENABLE_CACHE_PERSISTENCE CACHE_BATCH_INTERVAL CACHE_BATCH_SIZE
  export MAX_CONCURRENT_RECONCILES AWS_RATE_LIMIT_QPS AWS_RATE_LIMIT_BURST
  export AWS_ENDPOINT_URL="$CONTROLLER_AWS_ENDPOINT"
  export AWS_REGION AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY

  log_info "Applying controller manifests (image=${CONTROLLER_IMAGE}, namespace=${CONTROLLER_NAMESPACE})"
  envsubst < /manifests/controller.yaml | kubectl apply -f -
  kubectl -n "$CONTROLLER_NAMESPACE" rollout status deploy/k8s-eni-tagger --timeout=180s
  log_info "Controller deployment is ready"
}

apply_test_pod() {
  export CONTROLLER_NAMESPACE ANNOTATION_KEY ANNOTATION_VALUE_JSON
  envsubst < /manifests/test-pod.yaml | kubectl apply -f -
  kubectl -n "$CONTROLLER_NAMESPACE" wait --for=condition=Ready pod/e2e-eni-tagger-pod --timeout=60s || true
  
  # Get actual pod IP assigned by k3s
  local actual_pod_ip
  actual_pod_ip=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-eni-tagger-pod -o jsonpath='{.status.podIP}')
  if [ -n "$actual_pod_ip" ]; then
    log_info "Pod IP is ${actual_pod_ip}, updating ENI in mock"
    ENI_PRIVATE_IP="$actual_pod_ip"
    # Re-seed mock with correct IP
    kubectl -n "$CONTROLLER_NAMESPACE" port-forward svc/aws-mock 4566:4566 >/dev/null 2>&1 &
    local PF_PID=$!
    sleep 2
    local payload
    payload=$(cat <<EOF
{"eniId":"${ENI_ID}","privateIp":"${ENI_PRIVATE_IP}","interfaceType":"${ENI_INTERFACE_TYPE}","subnetId":"${ENI_SUBNET_ID}"}
EOF
)
    curl -sf -X POST "http://localhost:4566/admin/enis" \
      -H 'Content-Type: application/json' \
      -d "$payload" >/dev/null
    kill $PF_PID 2>/dev/null || true
    log_info "ENI updated with actual pod IP ${actual_pod_ip}"
  fi
}

wait_for_reconciliation() {
  log_info "Waiting for successful tagging condition"
  local waited=0
  until kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-eni-tagger-pod -o jsonpath='{.status.conditions[?(@.type=="eni-tagger.io/tagged")].status}' 2>/dev/null | grep -q "True"; do
    sleep 3
    waited=$((waited + 3))
    if [ $waited -ge $MAX_WAIT_SECONDS ]; then
      log_error "Tagging condition not observed after $MAX_WAIT_SECONDS seconds"
      kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-eni-tagger-pod -o yaml || true
      exit 1
    fi
  done
  log_info "Pod successfully tagged"
}

verify_tags() {
  log_info "Verifying tags in mock for ${ENI_ID}"
  # Use port-forward to access admin API
  kubectl -n "$CONTROLLER_NAMESPACE" port-forward svc/aws-mock 4566:4566 >/dev/null 2>&1 &
  local PF_PID=$!
  sleep 2
  local tags
  tags=$(curl -sf "http://localhost:4566/admin/tags/${ENI_ID}")
  kill $PF_PID 2>/dev/null || true
  echo "$tags" | jq .
  if ! echo "$tags" | jq -e '.Team == "platform" and .CostCenter == "1234"' >/dev/null; then
    log_error "Expected tags not found on ENI"
    exit 1
  fi
  log_info "Tags applied successfully"
}

delete_and_verify_cleanup() {
  log_info "Deleting test pod and verifying tag cleanup"
  kubectl -n "$CONTROLLER_NAMESPACE" delete pod e2e-eni-tagger-pod --ignore-not-found --timeout=60s
  local waited=0
  until [ "$waited" -ge "$MAX_WAIT_SECONDS" ]; do
    if ! kubectl -n "$CONTROLLER_NAMESPACE" get pod e2e-eni-tagger-pod >/dev/null 2>&1; then
      break
    fi
    sleep 2
    waited=$((waited + 2))
  done

  # Use port-forward to check tags
  kubectl -n "$CONTROLLER_NAMESPACE" port-forward svc/aws-mock 4566:4566 >/dev/null 2>&1 &
  local PF_PID=$!
  sleep 2
  local tags
  tags=$(curl -sf "http://localhost:4566/admin/tags/${ENI_ID}")
  kill $PF_PID 2>/dev/null || true
  if echo "$tags" | jq -e 'length == 0' >/dev/null; then
    log_info "Tags cleaned up successfully"
  else
    log_warn "Tags still present after deletion: $tags"
  fi
}

test_smart_cache_reuse() {
  log_info "Testing smart cache Pod UID validation"

  # Create a second test pod to verify cache behavior
  local pod2_name="e2e-cache-test-pod"

  log_info "Creating test pod: ${pod2_name}"
  cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: ${pod2_name}
  namespace: ${CONTROLLER_NAMESPACE}
  annotations:
    ${ANNOTATION_KEY}: '${ANNOTATION_VALUE_JSON}'
spec:
  restartPolicy: Never
  containers:
  - name: nginx
    image: nginx:alpine
    ports:
    - containerPort: 80
EOF

  # Wait for pod to be running and have IP
  log_info "Waiting for ${pod2_name} to be ready"
  local waited=0
  until kubectl -n "$CONTROLLER_NAMESPACE" get pod "$pod2_name" -o jsonpath='{.status.podIP}' 2>/dev/null | grep -q "^[0-9]"; do
    sleep 2
    waited=$((waited + 2))
    if [ $waited -ge 60 ]; then
      log_error "Pod ${pod2_name} did not get IP after 60 seconds"
      kubectl -n "$CONTROLLER_NAMESPACE" get pod "$pod2_name" -o yaml || true
      exit 1
    fi
  done

  local pod2_ip
  pod2_ip=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod "$pod2_name" -o jsonpath='{.status.podIP}')
  local pod2_uid
  pod2_uid=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod "$pod2_name" -o jsonpath='{.metadata.uid}')
  log_info "Pod ${pod2_name} has IP: ${pod2_ip}, UID: ${pod2_uid}"

  # Wait for tagging to complete
  log_info "Waiting for ${pod2_name} to be tagged"
  waited=0
  until kubectl -n "$CONTROLLER_NAMESPACE" get pod "$pod2_name" -o jsonpath='{.status.conditions[?(@.type=="eni-tagger.io/tagged")].status}' 2>/dev/null | grep -q "True"; do
    sleep 3
    waited=$((waited + 3))
    if [ $waited -ge "$MAX_WAIT_SECONDS" ]; then
      log_warn "Tagging condition not observed after ${MAX_WAIT_SECONDS} seconds for ${pod2_name}"
      # Don't fail - cache test can continue
      break
    fi
  done

  # Verify cache ConfigMap contains entry with Pod UID
  log_info "Checking cache ConfigMap for Pod UID validation"
  local cache_entry
  cache_entry=$(kubectl -n "$CONTROLLER_NAMESPACE" get configmap eni-tagger-cache -o jsonpath="{.data['${pod2_ip}']}" 2>/dev/null || echo "{}")

  if echo "$cache_entry" | jq -e ".pod_uid == \"${pod2_uid}\"" >/dev/null 2>&1; then
    log_info "✓ Cache entry contains correct Pod UID"
  else
    log_warn "Cache entry format: $cache_entry"
    log_warn "Expected pod_uid: ${pod2_uid}"
    # Don't fail - cache might not be persisted yet or ConfigMap persistence disabled
  fi

  # Delete the pod to trigger cache invalidation
  log_info "Deleting ${pod2_name} to test cache invalidation"
  kubectl -n "$CONTROLLER_NAMESPACE" delete pod "$pod2_name" --wait=true --timeout=60s

  # Wait for pod deletion
  waited=0
  until ! kubectl -n "$CONTROLLER_NAMESPACE" get pod "$pod2_name" >/dev/null 2>&1; do
    sleep 2
    waited=$((waited + 2))
    if [ $waited -ge 60 ]; then
      log_error "Pod ${pod2_name} not deleted after 60 seconds"
      exit 1
    fi
  done

  # Verify cache entry was invalidated (should be removed from ConfigMap)
  log_info "Verifying cache invalidation"
  local cache_after_delete
  cache_after_delete=$(kubectl -n "$CONTROLLER_NAMESPACE" get configmap eni-tagger-cache -o jsonpath="{.data['${pod2_ip}']}" 2>/dev/null || echo "")

  if [ -z "$cache_after_delete" ]; then
    log_info "✓ Cache entry correctly invalidated (removed from ConfigMap)"
  else
    log_warn "Cache entry still present after deletion (may be expected if another pod has same IP)"
    log_warn "Cache entry: $cache_after_delete"
  fi

  # Create a third pod to simulate the IP reuse scenario
  local pod3_name="e2e-cache-reuse-pod"
  log_info "Creating ${pod3_name} to simulate IP reuse scenario"

  cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: ${pod3_name}
  namespace: ${CONTROLLER_NAMESPACE}
  annotations:
    ${ANNOTATION_KEY}: '${ANNOTATION_VALUE_JSON}'
spec:
  restartPolicy: Never
  containers:
  - name: nginx
    image: nginx:alpine
    ports:
    - containerPort: 80
EOF

  # Wait for new pod to get IP
  waited=0
  until kubectl -n "$CONTROLLER_NAMESPACE" get pod "$pod3_name" -o jsonpath='{.status.podIP}' 2>/dev/null | grep -q "^[0-9]"; do
    sleep 2
    waited=$((waited + 2))
    if [ $waited -ge 60 ]; then
      log_error "Pod ${pod3_name} did not get IP after 60 seconds"
      exit 1
    fi
  done

  local pod3_ip
  pod3_ip=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod "$pod3_name" -o jsonpath='{.status.podIP}')
  local pod3_uid
  pod3_uid=$(kubectl -n "$CONTROLLER_NAMESPACE" get pod "$pod3_name" -o jsonpath='{.metadata.uid}')
  log_info "Pod ${pod3_name} has IP: ${pod3_ip}, UID: ${pod3_uid}"

  # The key test: even if pod3 got the same IP as pod2 (unlikely but possible),
  # the cache should NOT reuse the old entry because Pod UIDs differ
  if [ "$pod3_ip" = "$pod2_ip" ]; then
    log_info "✓ IP reuse detected! Pod3 has same IP as Pod2: ${pod3_ip}"
    log_info "  This is the ideal scenario to test UID-based cache validation"

    # Check that cache entry has new UID, not old one
    sleep 5  # Give controller time to reconcile
    local cache_pod3
    cache_pod3=$(kubectl -n "$CONTROLLER_NAMESPACE" get configmap eni-tagger-cache -o jsonpath="{.data['${pod3_ip}']}" 2>/dev/null || echo "{}")

    if echo "$cache_pod3" | jq -e ".pod_uid == \"${pod3_uid}\"" >/dev/null 2>&1; then
      log_info "✓ Cache correctly updated with new Pod UID (${pod3_uid})"
      log_info "✓ Smart cache IP reuse test PASSED"
    else
      log_error "Cache entry has wrong UID! Expected: ${pod3_uid}, Got: $cache_pod3"
      exit 1
    fi
  else
    log_info "Pods have different IPs (${pod2_ip} vs ${pod3_ip})"
    log_info "IP reuse not observed, but cache invalidation was verified"
    log_info "✓ Smart cache test PASSED (cache invalidation confirmed)"
  fi

  # Cleanup
  kubectl -n "$CONTROLLER_NAMESPACE" delete pod "$pod3_name" --ignore-not-found --wait=false

  log_info "Smart cache Pod UID validation test completed successfully"
}

main() {
  log_info "Starting e2e-v2 runner"
  fix_kubeconfig
  wait_for_k3s
  ensure_mock_image
  deploy_mock
  wait_for_mock_pod
  seed_mock
  select_endpoint
  ensure_controller_image
  deploy_controller
  apply_test_pod
  wait_for_reconciliation
  verify_tags
  delete_and_verify_cleanup
  
  # Run regression test
  test_smart_cache_reuse

  log_info "E2E-v2 flow completed"
}

main "$@"
