# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2025-12-08

### Added
- Initial release of k8s-eni-tagger
- Automatic ENI tagging based on Pod annotations
- Support for `eni-tagger.io/tags` annotation on Kubernetes Pods
- Multi-platform Docker images (amd64, arm64)
- Dry-run mode for safe testing
- Namespace filtering support
- Prometheus metrics for monitoring:
  - AWS API latency
  - Operation counts
  - Active worker tracking
- Health check endpoints (readiness probe support)
- Rate limiting configuration for AWS API calls
- Comprehensive logging and error handling
- Support for tag reconciliation (add missing, remove obsolete)
- GitHub Actions workflows:
  - Automated testing on push and pull requests
  - Docker image building and pushing to ghcr.io
  - Release workflow for tagged versions

### Security
- Minimal distroless base image (production build)
- Non-root user execution
- IAM permission scoping to only required EC2 operations

---

## How to Upgrade

To upgrade from a previous version, pull the latest image from ghcr.io:

```bash
docker pull ghcr.io/prabhu-mannu/k8s-eni-tagger:v0.1.0
```

Or using Helm:

```bash
helm upgrade k8s-eni-tagger ./charts/k8s-eni-tagger
```

## Versioning Policy

This project follows [Semantic Versioning 2.0.0](https://semver.org/):

- **MAJOR** version (X.0.0): Breaking changes to the API or behavior
- **MINOR** version (0.X.0): New features added in a backward-compatible manner
- **PATCH** version (0.0.X): Backward-compatible bug fixes

## Release Process

Releases are automated via GitHub Actions:

1. Tag a commit with semantic version: `git tag -a vX.Y.Z -m "Release vX.Y.Z"`
2. Push the tag: `git push origin vX.Y.Z`
3. The release workflow automatically:
   - Runs tests
   - Builds multi-platform Docker images
   - Pushes images to ghcr.io
   - Creates a GitHub Release with binaries and release notes
