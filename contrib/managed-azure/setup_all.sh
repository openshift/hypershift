#!/bin/bash
set -x

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${SCRIPT_DIR}/vars.sh"

# One-time setup scripts (comment out these three after first run to reuse resources)
"${SCRIPT_DIR}/setup_MIv3_kv.sh"
"${SCRIPT_DIR}/setup_oidc_provider.sh"
"${SCRIPT_DIR}/setup_dataplane_identities.sh"

# Per-cluster setup scripts
"${SCRIPT_DIR}/setup_aks_cluster.sh"
"${SCRIPT_DIR}/setup_external_dns.sh"
"${SCRIPT_DIR}/setup_install_ho_on_aks.sh"
"${SCRIPT_DIR}/create_basic_hosted_cluster.sh"

set +x