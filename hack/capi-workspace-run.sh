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
} > "$WORK_DIR/go.work"

cd "$WORK_DIR"
GOWORK="$WORK_DIR/go.work" GOFLAGS='' exec "$@"
