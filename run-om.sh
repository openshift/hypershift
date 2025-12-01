#! /bin/bash

set -ex

rm -rf input-dir
rm -rf test-output
mkdir test-output

# Build control-plane-operator
make control-plane-operator

# Check if authentication-operator is in PATH
if ! command -v authentication-operator &> /dev/null; then
    echo "Error: authentication-operator not found in PATH"
    exit 1
fi

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

# Extract guest cluster kubeconfig
echo "Extracting guest cluster kubeconfig..."
guest_kubeconfig="./kubeconfig.guest"
kubectl get secret -n "$hc_namespace" "${cluster_name}-admin-kubeconfig" -o jsonpath='{.data.kubeconfig}' | base64 -d > "$guest_kubeconfig"

if [[ ! -s "$guest_kubeconfig" ]]; then
    echo "Error: Failed to extract guest kubeconfig"
    exit 1
fi

echo "Guest kubeconfig saved to: $guest_kubeconfig"

# Use management cluster kubeconfig
mgmt_kubeconfig="${MGMT_KUBECONFIG:-${KUBECONFIG:-$HOME/.kube/config}}"

echo "Running Openshift Manager Operator..."
./bin/control-plane-operator om \
    --namespace "$hcp_namespace" \
    --hosted-control-plane "$cluster_name" \
    --input-dir input-dir \
    --output-dir test-output \
    --guest-cluster-kubeconfig "$guest_kubeconfig" \
    --management-cluster-kubeconfig "$mgmt_kubeconfig"

