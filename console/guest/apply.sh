#!/usr/bin/env bash
set -euo pipefail

# Apply in-guest-cluster API resources (CRD + oc-cli-downloads CR) using the
# guest kubeconfig. CRD first, then the CR.
cd "$(dirname "$0")"
export KUBECONFIG="$(cd ../hostedcluster && pwd)/kubeadmin.kubeconfig"

kubectl apply -f consoleclidownloads-crd.yaml
kubectl apply -f oc-cli-downloads.yaml
