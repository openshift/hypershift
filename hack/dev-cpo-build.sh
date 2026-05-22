#!/bin/bash
set -euo pipefail

TAG="${1:?Usage: $0 <tag>}"
IMAGE="quay.io/apahim/hypershift:${TAG}"
COMMIT_HASH=$(git rev-parse HEAD)

echo "Building control-plane-operator and control-plane-pki-operator for linux/amd64..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor \
  go build -gcflags='all=-N -l' \
  -ldflags "-X github.com/openshift/hypershift/support/supportedversion.commitHash=${COMMIT_HASH}" \
  -o bin/control-plane-operator ./control-plane-operator

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor \
  go build -gcflags='all=-N -l' \
  -ldflags "-X github.com/openshift/hypershift/support/supportedversion.commitHash=${COMMIT_HASH}" \
  -o bin/control-plane-pki-operator ./control-plane-pki-operator

echo "Building container image ${IMAGE}..."
podman build --platform linux/amd64 -f Containerfile.control-plane.dev -t "${IMAGE}" .

echo "Pushing ${IMAGE}..."
podman push "${IMAGE}"

echo "Done: ${IMAGE}"
