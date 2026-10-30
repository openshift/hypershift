#!/usr/bin/env bash
#
# capi-crd-gen.sh — generate CRD manifests into a temporary directory and only
# replace the destination once generation succeeds.
#
# Generating straight into the destination means clearing it first, so any
# failure (a CAPI version skew, an unresolvable module) leaves the shipped
# manifests deleted and the tree dirty. Staging the output keeps a failed run
# a no-op.
#
# Usage: capi-crd-gen.sh [--no-workspace] <dest-dir> <command> [args...]
#
#   <command> is run with output:crd:artifacts:config=<tempdir> appended. By
#   default it runs inside the CAPI Go workspace (capi-workspace-run.sh) so
#   that types spread across the main module and each pkg/capi/<provider>
#   module resolve. Pass --no-workspace for paths that are already reachable
#   from the main module, such as ./vendor/... .
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

USE_WORKSPACE=true
if [[ "${1:-}" == "--no-workspace" ]]; then
    USE_WORKSPACE=false
    shift
fi

DEST="${1:?usage: capi-crd-gen.sh [--no-workspace] <dest-dir> <command> [args...]}"
shift
[[ $# -gt 0 ]] || { echo "ERROR: no command given" >&2; exit 1; }

TMP_OUT=$(mktemp -d)
trap 'rm -rf "$TMP_OUT"' EXIT

if [[ "$USE_WORKSPACE" == true ]]; then
    "$ROOT/hack/capi-workspace-run.sh" "$@" output:crd:artifacts:config="$TMP_OUT"
else
    (cd "$ROOT" && "$@" output:crd:artifacts:config="$TMP_OUT")
fi

shopt -s nullglob
generated=("$TMP_OUT"/*.yaml)
if [[ ${#generated[@]} -eq 0 ]]; then
    echo "ERROR: generation produced no CRDs; leaving $DEST untouched" >&2
    exit 1
fi

mkdir -p "$DEST"
rm -f "$DEST"/*.yaml
cp "${generated[@]}" "$DEST/"
