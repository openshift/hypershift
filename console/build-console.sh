#!/usr/bin/env bash
set -euo pipefail

# Builds a custom console image = stock release-payload console + the patched
# bridge binary (off-cluster -ca-file trust fix, branch off-cluster-ca-file-trust
# in _console-research). Overlays only the recompiled bridge onto the stock
# image, so frontend assets / CLI wiring are unchanged. See
# console/Dockerfile.console-bridge-patch.

REPO=quay.io/patmarti/console
TAG=$(date +%y%m%d%H%M%S)

# Exact release-payload console digest currently in use (must match the
# images: transform in console/kustomize/pat-console/kustomization.yaml).
BASE_CONSOLE_IMAGE=quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:93f39a224f8b86f25fa1395008a52f901e374e207a71c42956315560e2987f8d

CONSOLE_SRC="$(cd "$(dirname "$0")/../_console-research" && pwd)"
SELF_DIR="$(cd "$(dirname "$0")" && pwd)"
DOCKERFILE="$SELF_DIR/Dockerfile.console-bridge-patch"
# Pull secret with quay.io auth for the ocp-v4.0-art-dev base image FROM.
AUTHFILE="$SELF_DIR/pull-secret.json"

podman build \
  --authfile "$AUTHFILE" \
  -f "$DOCKERFILE" \
  --build-arg BASE_CONSOLE_IMAGE="$BASE_CONSOLE_IMAGE" \
  -t "$REPO:$TAG" \
  "$CONSOLE_SRC"
podman push "$REPO:$TAG"

echo "Built and pushed $REPO:$TAG"
echo "Update console/kustomize/pat-console/kustomization.yaml images: REPLACE_CONSOLE_IMAGE_REGISTRY -> $REPO:$TAG"
