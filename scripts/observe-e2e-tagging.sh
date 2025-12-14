#!/usr/bin/env bash
# observe-e2e-tagging.sh - Watch tagging in real-time during E2E tests
set -euo pipefail

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log() { printf "${GREEN}[$(date +%H:%M:%S)]${NC} %s\n" "$*"; }
section() { printf "\n${BLUE}=== %s ===${NC}\n" "$*"; }

# Start e2e-v2 environment
section "Starting E2E-v2 Environment"
cd e2e-v2/compose
docker compose up -d

# Wait for services to be ready
log "Waiting for k3s to be healthy..."
sleep 15
docker exec k8s-eni-tagger-k3s kubectl get --raw /readyz || sleep 10

log "Waiting for AWS mock to be ready..."
sleep 5

section "Environment Ready - Starting Observation"

PROJECT_ROOT="$(cd ../.. && pwd)"
CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-k8s-eni-tagger:dev}"

ensure_controller_image() {
  section "Building controller image (${CONTROLLER_IMAGE})"
  if docker image inspect "${CONTROLLER_IMAGE}" >/dev/null 2>&1; then
    log "Image already present locally: ${CONTROLLER_IMAGE}"
  else
    log "Image not found locally, building via make docker-build"
    (cd "${PROJECT_ROOT}" && make docker-build IMG="${CONTROLLER_IMAGE}")
  fi

  section "Loading image into k3s (containerd)"
  docker save "${CONTROLLER_IMAGE}" | docker exec -i k8s-eni-tagger-k3s ctr images import -
  log "Image imported into k3s"
}

seed_eni() {
  log "Seeding ENI in mock..."
  docker exec k8s-eni-tagger-k3s kubectl -n default port-forward svc/aws-mock 4566:4566 >/dev/null 2>&1 &
  local PF_PID=$!
  sleep 2
  curl -s -X POST http://localhost:4566/admin/enis \
    -H 'Content-Type: application/json' \
    -d '{"eniId":"eni-1234","privateIp":"10.0.1.42","interfaceType":"interface","subnetId":"subnet-1234"}' | jq .
  kill $PF_PID 2>/dev/null || true
}

delete_test_pod() {
  log "Deleting test pod (triggers untag)..."
  docker exec k8s-eni-tagger-k3s kubectl delete pod e2e-eni-tagger-pod -n default --ignore-not-found=true || true
  # Wait for deletion to avoid AlreadyExists on recreate
  docker exec k8s-eni-tagger-k3s kubectl wait --for=delete pod/e2e-eni-tagger-pod -n default --timeout=20s >/dev/null 2>&1 || true
}

deploy_controller_and_pod() {
  section "Deploying controller and test Pod"
  delete_test_pod
  docker compose run --rm \
    -e LOAD_CONTROLLER_IMAGE_TAR=0 \
    -e CONTROLLER_IMAGE="${CONTROLLER_IMAGE}" \
    runner /runner/run.sh
}

# Function to query tags from mock
check_tags() {
  local eni_id=${1:-eni-1234}
  printf "\n${YELLOW}Current tags on %s:${NC}\n" "$eni_id"
  # Port-forward to aws-mock service in k3s
  docker exec k8s-eni-tagger-k3s kubectl -n default port-forward svc/aws-mock 4566:4566 >/dev/null 2>&1 &
  local PF_PID=$!
  sleep 2
  curl -s http://localhost:4566/admin/tags/$eni_id | jq -C . || echo "No tags yet"
  kill $PF_PID 2>/dev/null || true
}

# Show initial state
section "1. Initial State (Before Tagging)"
check_tags eni-1234

# Interactive menu
section "Interactive Observation"
echo "
Options:
1. Build & load controller image into k3s
2. Seed ENI + deploy controller/test pod (run flow)
3. Check current tags on ENI
4. View controller logs
5. View Pod details
6. View all Pods
7. Check AWS mock health
8. Delete test pod (untag)
9. Cleanup and exit

Press Ctrl+C to exit
"

while true; do
  read -p "Choose option (1-9): " choice
  case $choice in
    1)
      ensure_controller_image
      ;;
    2)
      seed_eni
      deploy_controller_and_pod
      ;;
    3)
      check_tags eni-1234
      ;;
    4)
      printf "\n${YELLOW}Last 30 controller logs:${NC}\n"
      docker logs k8s-eni-tagger-k3s 2>&1 | tail -30
      ;;
    5)
      printf "\n${YELLOW}Pod details:${NC}\n"
      docker exec k8s-eni-tagger-k3s kubectl describe pod e2e-eni-tagger-pod -n default 2>/dev/null || echo "Pod not found"
      ;;
    6)
      printf "\n${YELLOW}All Pods:${NC}\n"
      docker exec k8s-eni-tagger-k3s kubectl get pods -A
      ;;
    7)
      printf "\n${YELLOW}AWS Mock health:${NC}\n"
      curl -s http://localhost:4566/healthz && echo "" || echo "Mock unavailable"
      ;;
    8)
      delete_test_pod
      ;;
    9)
      log "Cleaning up..."
      cd ../.. && make e2e-v2-down
      exit 0
      ;;
    *)
      echo "Invalid option"
      ;;
  esac
done
