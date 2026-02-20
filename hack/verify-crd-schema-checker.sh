#!/usr/bin/env bash

# This script verifies that no breaking changes have been introduced to
# HyperShift CRD schemas. It compares the current CRD YAML files against
# a git base reference using the crd-schema-checker comparators.
#
# In CI (Prow), PULL_BASE_SHA is set automatically to the base commit.
# For local development, it defaults to the merge-base with origin/main.
#
# Breaking changes detected include:
#   - Field removals
#   - Enum value removals
#   - New required fields
#   - Data type changes
#
# Pre-existing violations can be overridden in Prow via /override.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TOOLS_DIR="${REPO_ROOT}/hack/tools"
TOOLS_BIN="${TOOLS_DIR}/bin"
CRD_SCHEMA_CHECK="${TOOLS_BIN}/crd-schema-check"

GO="${GO:-go}"
export GO111MODULE=on
export GOWORK=off
export GOFLAGS=-mod=vendor

# Build the crd-schema-check tool.
echo "Building crd-schema-check tool..."
(cd "${TOOLS_DIR}" && ${GO} build -tags=tools -o "${CRD_SCHEMA_CHECK}" ./cmd/crdschemacheck)

# Determine the comparison base.
if [[ -n "${PULL_BASE_SHA:-}" ]]; then
    COMPARISON_BASE="${PULL_BASE_SHA}"
    echo "Using CI comparison base: ${COMPARISON_BASE}"
else
    # Local development: use merge-base with origin/main or origin/master.
    COMPARISON_BASE=$(
        git merge-base HEAD origin/main 2>/dev/null ||
        git merge-base HEAD origin/master 2>/dev/null ||
        git merge-base HEAD origin/HEAD 2>/dev/null ||
        (git rev-parse --verify HEAD~1 2>/dev/null || git rev-parse HEAD)
    )
    echo "Using local comparison base: ${COMPARISON_BASE}"
fi

# Define the directories containing HyperShift CRDs.
CRD_DIRS="${REPO_ROOT}/cmd/install/assets/hypershift-operator"

echo "Checking CRD schemas for breaking changes..."
echo "  Comparison base: ${COMPARISON_BASE}"
echo "  CRD directories: ${CRD_DIRS}"
echo ""

exec "${CRD_SCHEMA_CHECK}" \
    --crd-dirs="${CRD_DIRS}" \
    --comparison-base="${COMPARISON_BASE}"
