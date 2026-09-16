#!/usr/bin/env bash
set -euo pipefail

# Applies the pat-console console overlay to the live HostedCluster.
# Layers: ../origin (vendored upstream) -> ../hypershift (Phase 1 patches) ->
# . (this cluster's values). See ../README.md.
#
# --load-restrictor LoadRestrictionsNone is required because this overlay's
# `resources:` pull individual files from ../hypershift/../origin — outside the
# kustomization root (console/kustomize/pat-console). Kustomize blocks
# out-of-root file reads by default as a sandboxing measure against untrusted
# kustomizations; safe to disable here since this is our own locally-authored
# overlay and files.

cd "$(dirname "$0")"

export KUBECONFIG="${KUBECONFIG:-/home/patmarti/.kube/config_gcp-hcp-int-mc-us-central1-yjiv}"

# Extract the OIDC client secret from the intact OAuth client JSON into a
# gitignored file the secretGenerator reads (keeps the JSON as the source of
# truth; nothing else touches its contents).
python3 -c "import json; print(json.load(open('../../guest/oauth_client_secret.json'))['web']['client_secret'], end='')" > .oidc-client-secret

# Session cookie keys for -user-auth=oidc: 32-byte AES key + 64-byte HMAC key.
# Generated once and kept (regenerating invalidates existing sessions).
[ -s .session-encryption-key ] || head -c 32 /dev/urandom > .session-encryption-key
[ -s .session-authentication-key ] || head -c 64 /dev/urandom > .session-authentication-key

kustomize build --load-restrictor LoadRestrictionsNone . | kubectl apply -f -
