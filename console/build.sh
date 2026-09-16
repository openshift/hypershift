#!/usr/bin/env bash
set -euo pipefail

REPO=quay.io/patmarti/hypershift-console-control-plane
TAG=$(date +%y%m%d%H%M%S)

cd "$(dirname "$0")/.."

# CPO-only dev image (console/Dockerfile.dev.fast) — a faster copy of
# ../Dockerfile.dev for iterating on control-plane-operator changes: it uses a
# persistent Go build-cache mount and builds only the CPO binaries, so
# incremental rebuilds recompile just the changed packages instead of doing a
# full cold compile of all six binaries. This image is used solely as the
# control-plane-operator-image override (see annotation update below), which is
# all the console spike needs. --layers is required for the cache mount to be
# reused across builds. Still avoids Dockerfile.control-plane's auth-gated base.
podman build --layers -f console/Dockerfile.dev.fast --build-arg COMMIT_HASH="$(git rev-parse HEAD)" -t "$REPO:$TAG" .
podman push "$REPO:$TAG"

echo "Built and pushed $REPO:$TAG"

HC_MANIFEST="console/hostedcluster/hostedcluster.yaml"
sed -i -E "s#(hypershift.openshift.io/control-plane-operator-image: ).*#\1${REPO}:${TAG}#" "$HC_MANIFEST"
echo "Updated annotation in $HC_MANIFEST -> ${REPO}:${TAG}"
