#!/bin/bash
set -euo pipefail

OUTDIR="${OUTDIR:-$PWD}"

# Using "-modtime 1" to make generate target deterministic. It sets all file
# time stamps to unix timestamp 1
GO111MODULE=on GOFLAGS=-mod=vendor go run github.com/kevinburke/go-bindata/go-bindata -mode 420 -modtime 1 \
  -pkg hypershift \
  -o ${OUTDIR}/hypershift-operator/assets/controlplane/hypershift/bindata.go \
  --prefix hypershift-operator/assets/controlplane/hypershift \
  --ignore bindata.go \
  hypershift-operator/assets/controlplane/hypershift/...

GO111MODULE=on GOFLAGS=-mod=vendor go run github.com/kevinburke/go-bindata/go-bindata -mode 420 -modtime 1 \
  -pkg roks \
  -o ${OUTDIR}/hypershift-operator/assets/controlplane/roks/bindata.go \
  --prefix hypershift-operator/assets/controlplane/roks \
  --ignore bindata.go \
  hypershift-operator/assets/controlplane/roks/...

gofmt -s -w ${OUTDIR}/hypershift-operator/assets/controlplane/hypershift/bindata.go
gofmt -s -w ${OUTDIR}/hypershift-operator/assets/controlplane/roks/bindata.go
