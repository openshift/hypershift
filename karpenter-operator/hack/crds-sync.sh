#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
ASSETS_DIR="${REPO_ROOT}/karpenter-operator/controllers/karpenter/assets"

cp "${REPO_ROOT}/vendor/github.com/aws/karpenter-provider-aws/pkg/apis/crds/"* "${ASSETS_DIR}/"
