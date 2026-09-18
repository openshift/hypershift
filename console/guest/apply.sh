#!/usr/bin/env bash
set -euo pipefail

# Apply the in-guest-cluster oc-cli-downloads CR using the guest kubeconfig.
# The consoleclidownloads CRD is no longer applied here: with the Console
# capability enabled, CVO installs it automatically (it carries
# capability.openshift.io/name: Console). The oc-cli-downloads CR itself is
# still applied by hand — the console-operator (which normally creates it) is
# stripped, so nothing reconciles it (other CLI-download CRs like helm/netobserv
# come from their own operators/CVO).
cd "$(dirname "$0")"
export KUBECONFIG="$(cd ../hostedcluster && pwd)/kubeadmin.kubeconfig"

kubectl apply -f oc-cli-downloads.yaml
