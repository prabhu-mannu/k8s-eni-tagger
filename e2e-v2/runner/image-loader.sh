#!/usr/bin/env bash
set -euo pipefail

log() {
  printf "[image-loader] %s\n" "$*"
}

TAR_PATH=${1:-${CONTROLLER_IMAGE_TAR:-/workspace/k8s-eni-tagger-dev.tar}}
K3S_CONTAINER=${K3S_CONTAINER_NAME:-k8s-eni-tagger-k3s}

if [ ! -S /var/run/docker.sock ]; then
  log "docker socket not available; skipping image import"
  exit 0
fi

if [ ! -f "$TAR_PATH" ]; then
  log "image tar not found at $TAR_PATH; skipping image import"
  exit 0
fi

log "Importing controller image tar $TAR_PATH into container $K3S_CONTAINER"

docker cp "$TAR_PATH" "$K3S_CONTAINER:/tmp/controller-image.tar"
docker exec "$K3S_CONTAINER" ctr -n k8s.io images import /tmp/controller-image.tar
# Clean up tar inside k3s container; ignore errors
if ! docker exec "$K3S_CONTAINER" rm -f /tmp/controller-image.tar; then
  log "warning: failed to remove temp tar inside k3s container"
fi

log "Image import completed"
