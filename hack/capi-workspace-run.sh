#!/usr/bin/env bash
# capi-workspace-run.sh — run a command inside a temporary Go workspace
# that includes the main module and all pkg/capi/<provider> modules.
#
# Usage: capi-workspace-run.sh <command> [args...]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

ln -s "$ROOT" "$WORK_DIR/hypershift"

# CAPI v1.14 moved its API types into a sibling sigs.k8s.io/cluster-api/api
# module, wired upstream with a relative "replace => ./api" and an
# unsatisfiable "require v0.0.0-00010101000000-000000000000". A dependency's
# own replace directives are ignored by consumers, so the placeholder survives
# into our graph. The root module resolves it via an explicit require, but
# workspace mode does not accept that and needs a replace. Empty on CAPI < 1.14,
# where no such module exists.
CAPI_API_VERSION=$(cd "$ROOT" && go list -m -f '{{.Version}}' sigs.k8s.io/cluster-api/api 2>/dev/null || true)

GO_VERSION=$(grep '^go ' "$ROOT/go.mod" | awk '{print $2}')
{
    echo "go $GO_VERSION"
    echo ""
    echo "use ("
    echo "    ./hypershift"
    echo "    ./hypershift/api"
    for mod_dir in $(find "$ROOT/pkg/capi" -name go.mod -exec dirname {} \; | sort); do
        echo "    ./hypershift/${mod_dir#"$ROOT/"}"
    done
    echo ")"
    if [[ -n "$CAPI_API_VERSION" ]]; then
        echo ""
        echo "replace sigs.k8s.io/cluster-api/api => sigs.k8s.io/cluster-api/api $CAPI_API_VERSION"
    fi
} > "$WORK_DIR/go.work"

cd "$WORK_DIR"
GOWORK="$WORK_DIR/go.work" GOFLAGS='' exec "$@"
