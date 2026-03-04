#!/bin/bash

set -e

export KUBECONFIG=/root/.kcli/clusters/hypershift/auth/kubeconfig

echo ""
echo "=========================================="
echo "Building HyperShift Binaries"
echo "=========================================="
echo ""

# Build all binaries using make
make hypershift product-cli control-plane-operator hypershift-operator

# Install CLIs
sudo install -m 0755 -o root -g root bin/hypershift /usr/local/bin/hypershift
sudo install -m 0755 -o root -g root bin/hcp /usr/local/bin/hcp

echo ""
echo "✓ Binaries built and installed"
echo ""

echo ""
echo "=========================================="
echo "Installing HyperShift Operator"
echo "=========================================="
echo ""

hypershift install \
  --enable-conversion-webhook=true \
  --wait-until-available

# Patch hypershift-operator deployment to use custom kubevirt CAPI provider image
oc set env deployment/operator -n hypershift \
  IMAGE_KUBEVIRT_CAPI_PROVIDER=quay.io/ramlavi/cluster-api-provider-kubevirt:4.18

# Wait for the operator to restart
oc rollout status deployment/operator -n hypershift

# Force rollout of capi-provider in all hosted cluster namespaces
echo "Rolling out capi-provider in all hosted cluster namespaces..."
for ns in $(oc get ns -l hypershift.openshift.io/hosted-control-plane -o jsonpath='{.items[*].metadata.name}'); do
  if oc get deployment capi-provider -n "$ns" &>/dev/null; then
    echo "  Restarting capi-provider in namespace: $ns"
    oc rollout restart deployment/capi-provider -n "$ns"
    oc rollout status deployment/capi-provider -n "$ns" --timeout=60s || true
  fi
done

echo "Done! All capi-provider deployments updated."

echo ""
echo "=========================================="
echo "Building Custom HyperShift Operator Image"
echo "=========================================="
echo ""

# Build and push custom hypershift-operator with UDN support
export QUAY_REPO_OP="quay.io/ramlavi/hypershift-operator"
export OP_IMAGE_TAG="udn-fix-$(date +%Y%m%d-%H%M%S)"
export OP_IMAGE="${QUAY_REPO_OP}:${OP_IMAGE_TAG}"
export REGISTRY_AUTH_FILE=/root/ralavi/merged-pull-secret.json

echo "Building container image: ${OP_IMAGE}"
# Explicitly pass auth file to podman build
REGISTRY_AUTH_FILE=/root/ralavi/merged-pull-secret.json podman build . -f Dockerfile -t ${OP_IMAGE}

echo ""
echo "Pushing to Quay.io..."
# Try to use existing podman auth, fall back to docker config if needed
if [ -f "/run/user/$(id -u)/containers/auth.json" ]; then
  podman push --authfile "/run/user/$(id -u)/containers/auth.json" ${OP_IMAGE}
elif [ -f "$HOME/.docker/config.json" ]; then
  podman push --authfile "$HOME/.docker/config.json" ${OP_IMAGE}
else
  podman push ${OP_IMAGE}
fi

echo ""
echo "Cleaning up old local images..."
# Remove old local images with the same repo but different tags (keep the one we just built)
podman images --filter "reference=${QUAY_REPO_OP}:udn-fix-*" --format "{{.ID}} {{.Repository}}:{{.Tag}}" | \
  grep -v "${OP_IMAGE_TAG}" | awk '{print $1}' | xargs -r podman rmi -f 2>/dev/null || true

echo ""
echo "✓ Custom operator image pushed: ${OP_IMAGE}"
echo ""

# Update the running hypershift-operator deployment
echo "Updating hypershift-operator deployment to use new image..."
# Use wildcard to update ALL containers at once (simpler and more reliable)
oc set image deployment/operator -n hypershift "*=${OP_IMAGE}"
oc rollout status deployment/operator -n hypershift

echo ""
echo "✓ HyperShift operator updated with UDN fix!"
echo ""

echo ""
echo "=========================================="
echo "Building Custom Control Plane Operator Image"
echo "=========================================="
echo ""

# Build and push custom control-plane-operator with UDN support
export QUAY_REPO="quay.io/ramlavi/hypershift-control-plane-operator"
export CPO_IMAGE_TAG="udn-fix-$(date +%Y%m%d-%H%M%S)"
export CPO_IMAGE="${QUAY_REPO}:${CPO_IMAGE_TAG}"

echo "Building container image: ${CPO_IMAGE}"
# Explicitly pass auth file to podman build
REGISTRY_AUTH_FILE=/root/ralavi/merged-pull-secret.json podman build -f Dockerfile.control-plane -t ${CPO_IMAGE} .

echo ""
echo "Pushing to Quay.io..."
# Try to use existing podman auth, fall back to docker config if needed
if [ -f "/run/user/$(id -u)/containers/auth.json" ]; then
  podman push --authfile "/run/user/$(id -u)/containers/auth.json" ${CPO_IMAGE}
elif [ -f "$HOME/.docker/config.json" ]; then
  podman push --authfile "$HOME/.docker/config.json" ${CPO_IMAGE}
else
podman push ${CPO_IMAGE}
fi

echo ""
echo "Cleaning up old local images..."
# Remove old local images with the same repo but different tags (keep the one we just built)
podman images --filter "reference=${QUAY_REPO}:udn-fix-*" --format "{{.ID}} {{.Repository}}:{{.Tag}}" | \
  grep -v "${CPO_IMAGE_TAG}" | awk '{print $1}' | xargs -r podman rmi -f 2>/dev/null || true

echo ""
echo "✓ Custom CPO image pushed: ${CPO_IMAGE}"
echo ""

# Save the image reference for cluster-sync.sh to use
echo "${CPO_IMAGE}" > ${PWD}/.cpo-image
echo "Image reference saved to .cpo-image"

echo ""
echo "=========================================="
echo "Updating Existing Clusters"
echo ""

# Apply the CPO image annotation to all existing HostedClusters
# This tells the hypershift-operator to use our custom CPO image
echo "Checking for existing hosted clusters..."
for hc in $(oc get hostedcluster --all-namespaces -o jsonpath='{range .items[*]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}'); do
  namespace=$(echo "$hc" | cut -d'/' -f1)
  name=$(echo "$hc" | cut -d'/' -f2)
  
  echo "  Updating HostedCluster: $namespace/$name"
  oc annotate hostedcluster "$name" -n "$namespace" \
    hypershift.openshift.io/control-plane-operator-image="${CPO_IMAGE}" \
    --overwrite
  
  # The hypershift-operator will automatically update the CPO deployment when it sees the annotation change
  # But we can speed things up by restarting the CPO pod
  if oc get pod -n "clusters-${name}" -l app=control-plane-operator &>/dev/null; then
    echo "    Restarting control-plane-operator pod..."
    oc delete pod -n "clusters-${name}" -l app=control-plane-operator
    echo "    Waiting for new pod to become ready..."
    oc wait --for=condition=ready pod -n "clusters-${name}" -l app=control-plane-operator --timeout=120s || echo "    Warning: CPO pod not ready yet, continuing..."
  fi
done

echo ""
echo "✓ All hosted clusters updated to use new CPO image: ${CPO_IMAGE}"
echo ""

echo ""
echo "=========================================="
echo "Cleaning Up Dangling Images"
echo "=========================================="
echo ""

# Remove dangling images (intermediate build layers with <none> tag)
echo "Removing dangling/intermediate build images..."
podman image prune -f

echo ""
echo "✓ Cleanup complete!"
echo ""
