
# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/prabhu/k8s-eni-tagger:latest
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.28.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized by category
.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint against code.
	golangci-lint run ./...

.PHONY: helm-lint
helm-lint: ## Lint Helm chart.
	helm lint charts/k8s-eni-tagger

.PHONY: test
test: fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

##@ E2E Testing

.PHONY: e2e
e2e: ## Run E2E tests using Docker Compose.
	cd e2e/compose && docker compose up --build --abort-on-container-exit

.PHONY: e2e-up
e2e-up: ## Start E2E environment (for debugging).
	cd e2e/compose && docker compose up --build -d

.PHONY: e2e-down
e2e-down: ## Stop and clean up E2E environment.
	cd e2e/compose && docker compose down -v

.PHONY: e2e-logs
e2e-logs: ## View E2E environment logs.
	cd e2e/compose && docker compose logs -f

.PHONY: e2e-probe
e2e-probe: ## Run capability probe to verify AWS mock readiness.
	cd e2e/compose && docker compose run --rm -e AWS_ENDPOINT_URL=http://moto:5000 -e AWS_REGION=us-west-2 -e AWS_ACCESS_KEY_ID=testing -e AWS_SECRET_ACCESS_KEY=testing runner /runner/capability-probe.sh

.PHONY: e2e-v2
e2e-v2: ## Run E2E-v2 tests (k3s + external AWS mock via compose).
	cd e2e-v2/compose && docker compose up --build --abort-on-container-exit

.PHONY: e2e-v2-up
e2e-v2-up: ## Start E2E-v2 environment (for debugging).
	cd e2e-v2/compose && docker compose up --build -d

.PHONY: e2e-v2-run
e2e-v2-run: ## Execute only the E2E-v2 runner against a running stack.
	cd e2e-v2/compose && docker compose run --rm runner /runner/run.sh

.PHONY: e2e-v2-down
e2e-v2-down: ## Stop and clean up E2E-v2 environment.
	cd e2e-v2/compose && docker compose down -v

.PHONY: e2e-v2-logs
e2e-v2-logs: ## Tail logs from E2E-v2 services.
	cd e2e-v2/compose && docker compose logs -f

##@ Build

.PHONY: build
build: fmt vet ## Build manager binary.
	go build -ldflags "-X main.version=$(shell git describe --tags --always --dirty) -X main.commit=$(shell git rev-parse --short HEAD) -X main.date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/manager main.go

.PHONY: run
run: fmt vet ## Run a controller from your host.
	go run ./main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

.PHONY: deploy
deploy: ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	kubectl apply -f deploy/manifests.yaml

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	kubectl delete -f deploy/manifests.yaml
