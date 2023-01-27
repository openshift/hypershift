#!/bin/bash

# This generation script sets up a fake GOPATH for docs generation
# to work around performance issues in the upstream code generation
# libraries. See:
#
#    https://github.com/kubernetes/gengo/issues/147
#    https://github.com/kubernetes/code-generator/issues/69

GEN_BIN="$1"
REPO_ROOT_DIR="$2"

FAKE_GOPATH="$(mktemp -d)"
trap 'rm -rf ${FAKE_GOPATH}' EXIT

FAKE_REPOPATH="${FAKE_GOPATH}/src/github.com/openshift/hypershift"
mkdir -p "$(dirname "${FAKE_REPOPATH}")" && ln -s "${REPO_ROOT_DIR}" "${FAKE_REPOPATH}"

cd "${FAKE_REPOPATH}"

export GOPATH="${FAKE_GOPATH}"
export GO111MODULE="off"

${GEN_BIN} \
--config "${FAKE_REPOPATH}/docs/api-doc-gen/config.json" \
--template-dir "${FAKE_REPOPATH}/docs/api-doc-gen/templates" \
--api-dir ./api/v1beta1 \
--out-file "${FAKE_REPOPATH}/docs/content/reference/api.md"
