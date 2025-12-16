# E2E-v2 Environment

E2E-v2 runs the k8s-eni-tagger controller **inside k3s** and talks to a lightweight AWS EC2 mock that runs alongside via Docker Compose. It exercises real Kubernetes behaviors (ServiceAccount/RBAC, finalizers, status patches, leader election) while keeping AWS calls local to the mock.

## Components

- **k3s** – Kubernetes API server + node, publishes kubeconfig via volume.
- **aws-mock** – Minimal EC2 Query API mock (DescribeAccountAttributes, DescribeNetworkInterfaces, CreateTags, DeleteTags) plus admin endpoints to seed and inspect ENIs.
- **runner** – Orchestrates the flow: waits for readiness, seeds the mock, imports the controller image (optional), applies manifests, patches Pod IPs, checks annotations/events, and validates tags.

Networking defaults to `host.docker.internal:4566` for the controller. When that is not reachable (e.g., Linux hosts without the alias), the runner creates a NodePort Service inside k3s that forwards to the mock container’s IP and points the controller to `http://<k3s-node-ip>:30066`.

## File map

- `compose/docker-compose.yaml` – Compose stack for k3s, aws-mock, and runner.
- `mock/` – Go-based EC2 mock (Dockerfile, main.go, README).
- `manifests/` – Controller Deployment/RBAC, optional NodePort wiring, and the test Pod.
- `runner/` – Runner image, `run.sh` orchestration script, `image-loader.sh` helper to import a local controller image into k3s containerd.
- `docs/` – This README + QUICKSTART instructions.

## Profiles

- **Baseline (default)**: leader election off, cache ConfigMap persistence off.
- **Full**: set `ENABLE_LEADER_ELECTION=true` and `ENABLE_CACHE_PERSISTENCE=true` before running the runner to exercise HA/cache persistence paths.

## Inputs

Key environment variables consumed by the runner:

- `CONTROLLER_IMAGE` (default `k8s-eni-tagger:dev`)
- `LOAD_CONTROLLER_IMAGE_TAR` (set to `1` to import a tarball via `image-loader.sh`)
- `CONTROLLER_IMAGE_TAR` (default `/workspace/k8s-eni-tagger-dev.tar`)
- `CONTROLLER_NAMESPACE` (default `default`)
- `TEST_POD_IP` / `ENI_PRIVATE_IP` (default `10.0.1.42`)
- `ANNOTATION_KEY` (default `eni-tagger.io/tags`)
- `ANNOTATION_VALUE_JSON` (default `{"Team":"platform","CostCenter":"1234"}`)
- `ENABLE_LEADER_ELECTION`, `ENABLE_CACHE_PERSISTENCE`, `CACHE_BATCH_INTERVAL`, `CACHE_BATCH_SIZE`

## What the flow validates

1. k3s API readiness and aws-mock health.
2. ENI seeding in the mock and DescribeNetworkInterfaces lookup by private IP.
3. Controller Deployment in-cluster using the chosen AWS endpoint.
4. Test Pod annotations, status IP patch, reconciliation annotations, and finalizer/cleanup paths.
5. Tag creation and removal via the mock’s admin API.

See `QUICKSTART.md` for concrete commands.
