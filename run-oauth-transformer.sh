#! /bin/bash

set -ex

# Build control-plane-operator
make control-plane-operator

echo "Fetching hosted clusters..."
selected=$(kubectl get hostedclusters -A -o json | jq -r '.items[] | "\(.metadata.namespace)/\(.metadata.name)"' | fzf --prompt="Select hosted cluster: ")

if [[ -z "$selected" ]]; then
    echo "No cluster selected"
    exit 1
fi

hc_namespace=$(echo "$selected" | cut -d'/' -f1)
cluster_name=$(echo "$selected" | cut -d'/' -f2)

echo "Selected: $cluster_name (namespace: $hc_namespace)"

# HostedControlPlane namespace follows the pattern clusters-<cluster-name>
hcp_namespace="clusters-${cluster_name}"
echo "HostedControlPlane namespace: $hcp_namespace"

# Use management cluster kubeconfig
mgmt_kubeconfig="${MGMT_KUBECONFIG:-${KUBECONFIG:-$HOME/.kube/config}}"

./bin/control-plane-operator om transform-deployment \
    --destination-deployment=destination-oauth-server-deployment.yaml \
    --source-deployment=./control-plane-operator/omoperator/cmd_transform_deployment_data/standalone-oauth-openshift.yaml \
    --target-deployment=./control-plane-operator/controllers/hostedcontrolplane/v2/assets/oauth-openshift/deployment.yaml \
    --namespace "$hcp_namespace" \
    --hosted-control-plane "$cluster_name" \
    --management-cluster-kubeconfig "$mgmt_kubeconfig"

cat destination-oauth-server-deployment.yaml

