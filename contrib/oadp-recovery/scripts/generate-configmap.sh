#!/usr/bin/env bash

set -euo pipefail

# Generate ConfigMap from the standalone script file
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTRIB_DIR="$(dirname "$SCRIPT_DIR")"
SCRIPT_FILE="$CONTRIB_DIR/oadp-recovery.sh"

if [[ ! -f "$SCRIPT_FILE" ]]; then
    echo "Error: Script file not found at $SCRIPT_FILE" >&2
    exit 1
fi

cat <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: oadp-recovery-script
  namespace: hypershift
data:
  oadp-recovery.sh: |
$(sed 's/^/    /' "$SCRIPT_FILE")
EOF