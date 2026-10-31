#!/usr/bin/env bash
set -euo pipefail

# This is also used before GHA allocates its job container. Keep one source of
# truth for the image instead of duplicating the digest in workflow YAML.
image=$(cat "$(dirname "${BASH_SOURCE[0]}")/verify-image")
if [[ ! $image =~ ^[a-z0-9][a-z0-9./:_-]*@sha256:[0-9a-f]{64}$ ]]; then
    echo "hack/verify-image must pin the verification image by digest." >&2
    exit 1
fi
printf '%s\n' "$image"
