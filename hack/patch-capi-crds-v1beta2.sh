#!/usr/bin/env bash
# patch-capi-crds-v1beta2.sh — post-generation patch for core CAPI CRDs.
#
# The vendored cluster-api (v1.10.x) only defines api/v1beta1, so controller-gen
# produces CRDs with a single served version.  The main branch bumped CAPI to
# 1.11+ which introduces the v1beta2 API group version.  Upgrade tests that run
# main's test binary against a release-4.21 management cluster need the
# v1beta2 API endpoint to exist, otherwise they fail with NoKindMatchError.
#
# This script is called by the Makefile `cluster-api` target after controller-gen
# to add a v1beta2 served (non-storage) version to every core cluster.x-k8s.io
# CRD.  The v1beta2 schema is identical to v1beta1 (the API is wire-compatible),
# and no conversion webhook is needed because the None strategy applies.
#
# See: https://issues.redhat.com/browse/OCPBUGS-122232
#      https://issues.redhat.com/browse/CNTRLPLANE-1200
#
# Usage:
#   hack/patch-capi-crds-v1beta2.sh <crd-output-dir>

set -euo pipefail

CRD_DIR="${1:?usage: $0 <crd-output-dir>}"

for crd_file in "${CRD_DIR}"/cluster.x-k8s.io_*.yaml; do
    [ -f "${crd_file}" ] || continue

    # Skip if the CRD already has a v1beta2 version entry.
    if yq -e '.spec.versions[] | select(.name == "v1beta2")' "${crd_file}" >/dev/null 2>&1; then
        continue
    fi

    echo "Patching ${crd_file}: adding v1beta2 served version"

    # Duplicate the v1beta1 version entry as v1beta2 with storage=false, served=true.
    # Ensure the original v1beta1 entry remains the storage version.
    yq -i '
        .spec.versions += [.spec.versions[] | select(.name == "v1beta1") | .name = "v1beta2" | .storage = false | .served = true] |
        (.spec.versions[] | select(.name == "v1beta1")).storage = true
    ' "${crd_file}"
done
