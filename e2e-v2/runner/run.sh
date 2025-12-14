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
LOAD_CONTROLLER_IMAGE_TAR=${LOAD_CONTROLLER_IMAGE_TAR:-0}
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
  if [ "$LOAD_CONTROLLER_IMAGE_TAR" != "1" ]; then
    log_info "Skipping image import (LOAD_CONTROLLER_IMAGE_TAR != 1)"
    return
  fi
  /runner/image-loader.sh "$CONTROLLER_IMAGE_TAR"
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
  log_info "E2E-v2 flow completed"
}

main "$@"
