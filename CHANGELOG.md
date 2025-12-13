# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Tag Namespace Prefixing**: New `--tag-namespace` flag for enterprise multi-tenant scenarios to prevent tag conflicts
- **NetworkPolicy Support**: Optional Kubernetes NetworkPolicy for controller pod network isolation
- **SecurityGroupPolicy CRD**: Proper AWS security groups for Pods support using EKS SecurityGroupPolicy (replaces annotation-based approach)
- **Automatic Pod Restarts**: Deployment includes checksum annotation for security group changes to trigger rolling updates
- **Pre-commit Hooks**: Added comprehensive pre-commit configuration with golangci-lint, helm lint, yamllint, markdownlint, and hadolint
- **Linting Targets**: New Makefile targets `make lint` and `make helm-lint` for code quality checks
- **golangci-lint Configuration**: Added `.golangci.yaml` with comprehensive linter settings
- **yamllint Configuration**: Added `.yamllint.yaml` for YAML validation rules
- **Pre-commit Setup Guide**: Comprehensive documentation in `docs/PRE_COMMIT_SETUP.md`

### Changed
- **IAM Policy**: Updated to include `ec2:DescribeAccountAttributes` permission for health checks
- **Security Group Binding**: Migrated from `vpc.amazonaws.com/pod-eni` annotation to `SecurityGroupPolicy` CRD (breaking change for security group users)
- **Documentation**: Comprehensive updates to README.md, ARCHITECTURE.md, and Helm chart README with new features
- **AWS Best Practices**: Enhanced documentation for PascalCase tag naming convention and AWS tagging guidelines

### Fixed
- Corrected IAM permissions documentation (was missing health check permissions)
- Fixed security group binding documentation (now uses correct SecurityGroupPolicy CRD format)
- Improved conditional rendering in Helm templates for optional features

### Security
- NetworkPolicy enforces ingress/egress rules for controller pods
- SecurityGroupPolicy provides AWS-native firewall at pod level
- Tag namespace prefixing prevents cross-tenant tag conflicts

## [0.1.1] - 2025-12-11

### Fixed
- Correct metrics container port in Helm chart to use the metrics port instead of the health probe port.

---

## [0.1.0] - 2024-12-09

### Added
- Initial release of k8s-eni-tagger controller
- Automatic ENI tagging based on Pod annotations
- AWS EC2 API integration for ENI management
- Comprehensive Helm chart with full parameter configurability
- IRSA (IAM Roles for Service Accounts) support
- Service account customization (create/use existing)
- EKS security group binding support via pod annotations
- Automatic leader election when replicaCount > 1
- Metrics Service with Prometheus annotations
- ConfigMap-based cache persistence with conditional RBAC
- Health probes (liveness/readiness) configuration
- Resource limits and requests configuration
- Node selector, tolerations, and affinity support
- Pod security context configuration
- All controller flags exposed as configurable values
- Production-ready example configuration (values-example.yaml)
- Multi-channel Helm distribution (OCI registry, GitHub Pages, GitHub Release)
- Multi-platform Docker images (amd64, arm64)
- Dry-run mode for safe testing
- Namespace filtering support
- Prometheus metrics for monitoring (AWS API latency, operation counts, active workers)
- Health check endpoints (readiness probe support)
- Rate limiting configuration for AWS API calls
- Comprehensive logging and error handling
- Support for tag reconciliation (add missing, remove obsolete)

### CI/CD
- GitHub Actions workflow for automated testing
- Docker multi-platform builds (amd64, arm64)
- Helm chart packaging and OCI registry push
- Automated release creation with all assets
- Docker image building and pushing to ghcr.io

### Security
- Minimal distroless base image (production build)
- Non-root user execution
- IAM permission scoping to only required EC2 operations

---

## Versioning Policy

This project follows [Semantic Versioning 2.0.0](https://semver.org/):

- **Chart version matches release tag**: For simplicity, the Helm chart version is synchronized with the application version and release tag (e.g., chart 0.1.1 = app 0.1.1 = release v0.1.1)
- **MAJOR** version (X.0.0): Breaking changes to the API or behavior
- **MINOR** version (0.X.0): New features added in a backward-compatible manner
- **PATCH** version (0.0.X): Backward-compatible bug fixes

## How to Upgrade

### Using Helm (OCI Registry)

```bash
# Upgrade to latest version
helm upgrade k8s-eni-tagger oci://ghcr.io/prabhu-mannu/charts/k8s-eni-tagger \
  --version 0.1.1 \
  --namespace kube-system
```

### Using Docker

```bash
docker pull ghcr.io/prabhu-mannu/k8s-eni-tagger:0.1.1
```

### Using kubectl with Manifests

```bash
kubectl apply -f https://github.com/prabhu-mannu/k8s-eni-tagger/releases/download/v0.1.1/manifests.yaml
```

## Release Process

Releases are automated via GitHub Actions:

1. Tag a commit with semantic version: `git tag -a vX.Y.Z -m "Release vX.Y.Z"`
2. Push the tag: `git push origin vX.Y.Z`
3. The release workflow automatically:
   - Runs tests
   - Builds multi-platform Docker images
   - Pushes images to ghcr.io
   - Creates a GitHub Release with binaries and release notes
