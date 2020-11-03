#!/bin/bash
set -e
set -u
set -o pipefail

TMP_DIR=$(mktemp -d)

function cleanup() {
    return_code=$?
    rm -rf "${TMP_DIR}"
    exit "${return_code}"
}
trap "cleanup" EXIT

OUTDIR=${TMP_DIR} ./hack/update-generated-bindata.sh

diff -Naup {.,${TMP_DIR}}/hypershift-operator/assets/controlplane/hypershift/bindata.go
diff -Naup {.,${TMP_DIR}}/hypershift-operator/assets/controlplane/roks/bindata.go
