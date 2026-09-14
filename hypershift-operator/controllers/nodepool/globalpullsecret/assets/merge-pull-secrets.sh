#!/bin/bash
# Merges /var/lib/kubelet/config.json with all *.json files in /var/lib/kubelet/auth.d/
# Output: /var/lib/kubelet/config-merged.json
# Base config.json entries take precedence over drop-in entries for conflicting registries.

set -euo pipefail

BASE="/var/lib/kubelet/config.json"
AUTH_D="/var/lib/kubelet/auth.d"
OUTPUT="/var/lib/kubelet/config-merged.json"

if [[ ! -f "$BASE" ]]; then
    echo "Base config $BASE not found, skipping merge" >&2
    exit 0
fi

if ! command -v jq &>/dev/null; then
    echo "jq not found, copying base config as-is" >&2
    cp "$BASE" "$OUTPUT"
    chmod 0600 "$OUTPUT"
    exit 0
fi

MERGED=$(cat "$BASE")

shopt -s nullglob
for f in "$AUTH_D"/*.json; do
    [[ -s "$f" ]] || continue
    # Drop-in auths are merged first, then base auths override (base wins for conflicts)
    MERGED=$(echo "$MERGED" | jq --slurpfile dropin "$f" '
        .auths = ($dropin[0].auths // {}) + .auths
    ')
done
shopt -u nullglob

# Write atomically
TMP=$(mktemp "${OUTPUT}.XXXXXX")
echo "$MERGED" > "$TMP"
chmod 0600 "$TMP"
mv "$TMP" "$OUTPUT"
