#!/usr/bin/env bash
set -euo pipefail

# CI uses ephemeral storage; the local launcher mounts a persistent cache volume.
cache=${VERIFY_CACHE_DIR:-${TMPDIR:-/tmp}/hypershift-verify-$(id -u)}
# Refuse pre-created symlinks, foreign owners and shared writable caches before
# adding cached executables to PATH. Support both GNU and macOS stat.
(umask 077; mkdir -p "$cache")
if [[ -L $cache || ! -d $cache || ! -O $cache ]]; then
    echo "Unsafe verification cache: $cache; set VERIFY_CACHE_DIR to a private directory you own." >&2
    exit 1
fi
mode=$(stat -c %a "$cache" 2>/dev/null || stat -f %Lp "$cache")
if (( (8#$mode & 077) != 0 )); then
    echo "Verification cache must have mode 700: $cache; set VERIFY_CACHE_DIR to a private directory you own." >&2
    exit 1
fi
export VERIFY_CONTAINER=0
export HOME="$cache/home"
export GOPATH="$cache/go"
export GOCACHE="${GOCACHE:-$cache/go-build}"
export XDG_CACHE_HOME="$cache/analysis"
export GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-$cache/golangci-lint}"
export TMPDIR="$cache/tmp"
export PATH="$GOPATH/bin:$PATH"
mkdir -p "$HOME" "$GOPATH" "$GOCACHE" "$XDG_CACHE_HOME" "$GOLANGCI_LINT_CACHE" "$TMPDIR"

# Prow can assign a UID different from the source layer's owner. Trust only this
# checkout through process-local configuration, preserving any existing entries.
config_index=${GIT_CONFIG_COUNT:-0}
export "GIT_CONFIG_KEY_$config_index=safe.directory"
export "GIT_CONFIG_VALUE_$config_index=$(pwd -P)"
export GIT_CONFIG_COUNT=$((config_index + 1))

if [[ $# == 0 ]]; then
    set -- make _verify
fi
exec "$@"
