# E2E-v2 QUICKSTART

This guide spins up k3s + the AWS mock via Docker Compose and runs the in-cluster controller flow.

## Prerequisites

- Docker + Compose v2
- `make`
- Optional: controller image tarball at `k8s-eni-tagger-dev.tar` (root) if you want to import a local build into k3s

## One-shot run

```bash
# From repo root
make e2e-v2
```

This builds the mock/runner images, starts k3s and the mock, runs the runner, and tears down when the runner exits.

## Bring up, iterate, and tear down

```bash
# Start stack in background (k3s + aws-mock + runner base image)
make e2e-v2-up

# Run the scripted flow against the running stack
make e2e-v2-run

# View logs
make e2e-v2-logs

# Clean up
make e2e-v2-down
```

## Controller image options

- Pullable image: set `CONTROLLER_IMAGE` to a registry reference (default `k8s-eni-tagger:dev`).
- Local tarball import: build and save an image at repo root:
  ```bash
  docker build -t k8s-eni-tagger:dev .
  docker save k8s-eni-tagger:dev -o k8s-eni-tagger-dev.tar
  CONTROLLER_IMAGE=k8s-eni-tagger:dev LOAD_CONTROLLER_IMAGE_TAR=1 make e2e-v2-run
  ```
  The runner mounts the Docker socket and uses `image-loader.sh` to import the tarball into the k3s containerd instance.

## Fallback endpoint (NodePort) on Linux

If `host.docker.internal:4566` is unreachable from Pods, the runner will:
1) Resolve the aws-mock container IP
2) Apply `manifests/nodeport-aws-mock.yaml` with that IP
3) Point the controller to `http://<k3s-node-ip>:30066`

## Tuning knobs

Environment variables you can set before `make e2e-v2*`:

- `TEST_POD_IP` (default `10.0.1.42`)
- `ANNOTATION_KEY` / `ANNOTATION_VALUE_JSON`
- `ENABLE_LEADER_ELECTION` (`true|false`)
- `ENABLE_CACHE_PERSISTENCE` (`true|false`)
- `CACHE_BATCH_INTERVAL` (e.g., `2s`), `CACHE_BATCH_SIZE`
- `MAX_CONCURRENT_RECONCILES`, `AWS_RATE_LIMIT_QPS`, `AWS_RATE_LIMIT_BURST`

## Expected outcomes

- Pod gets reconciliation annotations (`eni-tagger.io/last-reconciled-at`, hash)
- aws-mock shows tags for the seeded ENI via `/admin/tags/<eniId>`
- After Pod deletion, tags are removed and finalizers cleared

## Troubleshooting

- Check service logs: `make e2e-v2-logs`
- Inspect Pod state: `kubectl --kubeconfig e2e-v2/compose/kubeconfig/kubeconfig.yaml get pods -A`
- Verify mock health: `curl -sf http://localhost:4566/healthz`
- Ensure Docker socket is mounted if using local image import
