# Copilot Instructions for k8s-eni-tagger

This guide enables AI coding agents to be immediately productive in the k8s-eni-tagger codebase. It summarizes architecture, workflows, conventions, and integration points unique to this project.

## Project Overview
- **Purpose:** Kubernetes controller for automatic AWS ENI tagging based on Pod annotations.
- **Main Components:**
  - `pkg/controller/`: Pod event handling, annotation parsing, ENI resolution, tag syncing.
  - `pkg/aws/`: EC2 API client, rate limiting, retries.
  - `pkg/cache/`: ENI cache (in-memory, optional ConfigMap persistence).
  - `main.go`: Entrypoint, flag parsing, controller setup.
  - `charts/k8s-eni-tagger/`: Helm chart for deployment/configuration.

## Architecture & Data Flow
- Pod annotations trigger controller reconciliation.
- Controller parses annotations, resolves ENIs, and syncs tags via AWS EC2 API.
- ENI cache optimizes lookups; optionally persisted in ConfigMap.
- Metrics and health endpoints exposed for monitoring.

## Developer Workflows
- **Build:** Use `make` (see `Makefile`). Example: `make build`.
- **Test:** Run `make test` for unit tests. Coverage in `pkg/*`.
- **Debug:** Use `--dry-run` flag to simulate tagging without AWS changes.
- **Deploy:** Helm chart (`charts/k8s-eni-tagger/`) or manifests (`deploy/manifests.yaml`).
- **Configuration:** Controller flags (see README.md table) and Helm values.

## Conventions & Patterns
- **Pod Annotation Key:** `eni-tagger.io/tags` (configurable).
- **IAM Permissions:** Requires `ec2:DescribeNetworkInterfaces`, `ec2:CreateTags`, `ec2:DeleteTags`.
- **Metrics:** Prometheus at `/metrics`.
- **Health Probes:** `/healthz`, `/readyz` endpoints.
- **Leader Election:** Enabled for HA; see controller setup.
- **Cache:** ENI cache enabled by default; can persist to ConfigMap.
- **Rate Limiting:** AWS API QPS/burst configurable via flags.

## Integration Points
- **Kubernetes:** Watches Pod events, emits Kubernetes Events.
- **AWS:** EC2 API for ENI operations.
- **Prometheus:** Metrics endpoint for monitoring.
- **Helm:** Chart for deployment/configuration.

## Key Files & Directories
- `main.go`: Entrypoint, flag parsing.
- `pkg/controller/pod_controller.go`: Core reconciliation logic.
- `pkg/aws/client.go`: AWS EC2 API interactions.
- `pkg/cache/cache.go`: ENI cache implementation.
- `charts/k8s-eni-tagger/values.yaml`: Helm config options.
- `ARCHITECTURE.md`: Detailed architecture.

## Example: Pod Annotation for Tagging
```yaml
metadata:
  annotations:
    eni-tagger.io/tags: "CostCenter=1234,Team=Platform"
```

## Troubleshooting
- If ENIs are not tagged, check annotation key, IAM permissions, and ENI sharing settings.
- Monitor health and metrics endpoints for status and performance.

---
For more details, see `README.md`, `ARCHITECTURE.md`, and Helm chart documentation.
