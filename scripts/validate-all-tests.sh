#!/usr/bin/env bash
# validate-all-tests.sh - Run complete test suite for k8s-eni-tagger
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_section() { printf "\n${BLUE}========================================${NC}\n"; printf "${BLUE}%s${NC}\n" "$*"; printf "${BLUE}========================================${NC}\n\n"; }
log_success() { printf "${GREEN}✓ %s${NC}\n" "$*"; }
log_warning() { printf "${YELLOW}⚠ %s${NC}\n" "$*"; }
log_error() { printf "${RED}✗ %s${NC}\n" "$*"; }

FAILED=0

run_test() {
  local name=$1
  shift
  printf "\n${YELLOW}Running: %s${NC}\n" "$name"
  if "$@"; then
    log_success "$name passed"
    return 0
  else
    log_error "$name failed"
    FAILED=$((FAILED + 1))
    return 1
  fi
}

log_section "K8s ENI Tagger - Full Test Suite Validation"

# 1. Unit Tests
log_section "1. Unit Tests (go test)"
run_test "Unit tests with coverage" make test

# 2. Check coverage stats
log_section "2. Coverage Analysis"
go tool cover -func=cover.out | tail -20
TOTAL_COV=$(go tool cover -func=cover.out | grep total: | awk '{print $3}' | sed 's/%//')
if (( $(echo "$TOTAL_COV > 70" | bc -l) )); then
  log_success "Overall coverage: ${TOTAL_COV}% (target: >70%)"
else
  log_warning "Coverage at ${TOTAL_COV}% (below 70% target)"
fi

# 3. AWS Mock Build
log_section "3. AWS Mock Compilation"
run_test "AWS mock builds" bash -c "cd e2e-v2/mock && go build -o /tmp/aws-mock-test ."

# 4. Controller Image Build
log_section "4. Controller Docker Image"
if docker images | grep -q "k8s-eni-tagger:dev"; then
  log_success "Controller image k8s-eni-tagger:dev exists"
else
  log_warning "Controller image not found; building..."
  run_test "Build controller image" docker build -t k8s-eni-tagger:dev .
fi

# 5. Scenario Coverage Check
log_section "5. Documented Scenario Coverage"
echo "Checking test files for scenario coverage..."

check_scenario() {
  local scenario=$1
  local test_pattern=$2
  if grep -rq "$test_pattern" pkg/controller/*_test.go; then
    log_success "Scenario covered: $scenario"
  else
    log_warning "No explicit test for: $scenario"
  fi
}

check_scenario "No Annotation Skip" "No_Annotation.*Skip"
check_scenario "First Annotation Applied" "Success.*Tag"
check_scenario "Tag Deletion" "Deletion.*Untag"
check_scenario "Hash Conflict" "ConflictDetection\|checkHashConflict"
check_scenario "Foreign Tags Preserved" "ForeignTagsPreservation"
check_scenario "Idempotent Operations" "Nothing changed"

# 6. Test completeness
log_section "6. Test File Inventory"
echo "Controller test files:"
ls -1 pkg/controller/*_test.go | sed 's/^/  - /'
CONTROLLER_TEST_COUNT=$(ls -1 pkg/controller/*_test.go | wc -l | tr -d ' ')
log_success "Found $CONTROLLER_TEST_COUNT controller test files"

# 7. E2E readiness check
log_section "7. E2E Environment Readiness"
if [ -f "e2e-v2/compose/docker-compose.yaml" ] && [ -f "e2e-v2/runner/run.sh" ]; then
  log_success "E2E-v2 environment files present"
else
  log_error "E2E-v2 environment incomplete"
  FAILED=$((FAILED + 1))
fi

if [ -f "e2e/compose/docker-compose.yaml" ]; then
  log_success "E2E (moto) environment files present"
else
  log_warning "E2E (moto) environment incomplete"
fi

# 8. Linting
log_section "8. Code Quality Checks"
run_test "go fmt check" bash -c "test -z \$(gofmt -l .)"
run_test "go vet" go vet ./...

# Summary
log_section "Test Summary"
if [ $FAILED -eq 0 ]; then
  log_success "All validations passed!"
  echo ""
  echo "Test suite is complete and ready. To run E2E tests:"
  echo "  - E2E (moto):        make e2e"
  echo "  - E2E-v2 (custom):   make e2e-v2"
  exit 0
else
  log_error "$FAILED validation(s) failed"
  exit 1
fi
