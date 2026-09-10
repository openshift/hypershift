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
# Implementation note: uses only grep/sed/tail (no yq) so the script works in
# the CI verify image which ships the Go toolchain but not yq.  The sed
# patterns target the 4-space-indented version-entry fields produced by
# controller-gen; deeper-indented occurrences in the OpenAPI schema are
# unaffected.
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

    # Skip if the CRD already has a v1beta2 version entry (idempotency).
    if grep -q '^    name: v1beta2$' "${crd_file}"; then
        continue
    fi

    echo "Patching ${crd_file}: adding v1beta2 served version"

    # The controller-gen output has exactly one version entry under
    # spec.versions (v1beta1).  Copy that entry, rename it to v1beta2,
    # and mark it as non-storage.
    #
    # Layout produced by controller-gen:
    #   spec:
    #     ...
    #     versions:            ← 2-space indent
    #     - name: v1beta1      ← version entry starts here (2-space + "- ")
    #       ...
    #       served: true       ← 4-space indent, unique to version entry
    #       storage: true      ← 4-space indent, unique to version entry
    #       ...
    #   (EOF)
    #
    # "versions:" is the last top-level key under spec, so everything from
    # the first list item ("  - ") to EOF is the single version entry.
    versions_line=$(grep -n '^  versions:$' "${crd_file}" | head -1 | cut -d: -f1)
    version_start=$((versions_line + 1))

    # Extract the v1beta1 entry into a variable first, then append.
    # (Reading and appending to the same file in a pipeline would loop.)
    v1beta2_entry=$(tail -n +"${version_start}" "${crd_file}" \
        | sed -e 's/^    name: v1beta1$/    name: v1beta2/' \
              -e 's/^    storage: true$/    storage: false/')

    printf '%s\n' "${v1beta2_entry}" >> "${crd_file}"
done
