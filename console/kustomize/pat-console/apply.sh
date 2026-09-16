#!/usr/bin/env bash
set -euo pipefail

# Applies the pat-console console overlay to the live HostedCluster.
# Layers: ../origin (vendored upstream) -> ../hypershift (Phase 1 patches) ->
# . (this cluster's values). See ../README.md.
#
# --load-restrictor LoadRestrictionsNone is required because this overlay's
# secretGenerator reads ../../hostedcluster/console-guest-token.txt, and its
# `resources:` pull individual files from ../hypershift/../origin — all
# outside the kustomization root (console/kustomize/pat-console). Kustomize
# blocks out-of-root file reads by default as a sandboxing measure against
# untrusted kustomizations; safe to disable here since this is our own
# locally-authored overlay and file.

cd "$(dirname "$0")"

export KUBECONFIG="${KUBECONFIG:-/home/patmarti/.kube/config_gcp-hcp-int-mc-us-central1-yjiv}"

kustomize build --load-restrictor LoadRestrictionsNone . | kubectl apply -f -
