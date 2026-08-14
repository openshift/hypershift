#!/usr/bin/env bash

set -euo pipefail

# Simple test script without complex kustomize - just apply YAML directly

NUM_CLUSTERS=${NUM_CLUSTERS:-2}
NAMESPACE="test-oadp-recovery"

echo "🚀 Creating $NUM_CLUSTERS test clusters directly"

# Create namespace
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# Function to create a single cluster
create_cluster() {
    local cluster_num=$1
    local cluster_name="test-cluster-$(printf "%02d" $cluster_num)"
    local infra_id="$cluster_name-$(openssl rand -hex 3)"

    # Determine if paused (odd numbers)
    local is_paused=$((cluster_num % 2))
    local pause_annotations=""
    local pause_until=""

    if [[ $is_paused -eq 1 ]]; then
        pause_annotations="    oadp.openshift.io/paused-by: \"hypershift-oadp-plugin\"
    oadp.openshift.io/paused-at: \"true\""
        pause_until="  pausedUntil: \"true\""
    fi

    echo "  📦 Creating $cluster_name (paused: $([[ $is_paused -eq 1 ]] && echo "true" || echo "false"))"

    # Create secrets first
    kubectl create secret generic "$cluster_name-pull-secret" \
        --from-literal='.dockerconfigjson={"auths":{}}' \
        --type=kubernetes.io/dockerconfigjson \
        -n "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

    kubectl create secret generic "$cluster_name-ssh-key" \
        --from-literal='id_rsa.pub=ssh-rsa AAAAB3NzaC1yc2EAAAA dummy-key' \
        -n "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

    kubectl create secret generic "$cluster_name-etcd-encryption-key" \
        --from-literal="key=$(openssl rand -base64 32)" \
        -n "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

    # Create HostedCluster YAML
    cat <<EOF | kubectl apply --validate=false -f -
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: $cluster_name
  namespace: $NAMESPACE
  annotations:
    hypershift.openshift.io/cluster: $NAMESPACE/$cluster_name
$pause_annotations
spec:
  infraID: $infra_id
  clusterID: $(uuidgen | tr '[:upper:]' '[:lower:]')
  controllerAvailabilityPolicy: SingleReplica
$pause_until
  dns:
    baseDomain: test.example.com
    privateZoneID: Z$(openssl rand -hex 16)
    publicZoneID: Z$(openssl rand -hex 16)
  etcd:
    managed:
      storage:
        persistentVolume:
          size: 8Gi
          storageClassName: gp3-csi
        type: PersistentVolume
    managementType: Managed
  fips: false
  infrastructureAvailabilityPolicy: SingleReplica
  issuerURL: https://test-oidc.s3.us-west-2.amazonaws.com/$infra_id
  networking:
    clusterNetwork:
    - cidr: 10.$((100 + cluster_num)).0.0/14
    machineNetwork:
    - cidr: 10.$((200 + cluster_num)).0.0/16
    networkType: OVNKubernetes
    serviceNetwork:
    - cidr: 172.$((30 + cluster_num)).0.0/16
  olmCatalogPlacement: management
  platform:
    aws:
      cloudProviderConfig:
        subnet:
          id: subnet-$(openssl rand -hex 8)
        vpc: vpc-$(openssl rand -hex 8)
        zone: us-west-2a
      endpointAccess: Public
      multiArch: false
      region: us-west-2
      rolesRef:
        controlPlaneOperatorARN: arn:aws:iam::123456789012:role/$infra_id-control-plane-operator
        imageRegistryARN: arn:aws:iam::123456789012:role/$infra_id-openshift-image-registry
        ingressARN: arn:aws:iam::123456789012:role/$infra_id-openshift-ingress
        kubeCloudControllerARN: arn:aws:iam::123456789012:role/$infra_id-cloud-controller
        networkARN: arn:aws:iam::123456789012:role/$infra_id-cloud-network-config-controller
        nodePoolManagementARN: arn:aws:iam::123456789012:role/$infra_id-node-pool
        storageARN: arn:aws:iam::123456789012:role/$infra_id-aws-ebs-csi-driver-controller
    type: AWS
  pullSecret:
    name: $cluster_name-pull-secret
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.21.0-ec.3-x86_64
  secretEncryption:
    aescbc:
      activeKey:
        name: $cluster_name-etcd-encryption-key
    type: aescbc
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
  - service: Ignition
    servicePublishingStrategy:
      type: Route
  - service: Konnectivity
    servicePublishingStrategy:
      type: Route
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
  sshKey:
    name: $cluster_name-ssh-key
---
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: $cluster_name-workers-1
  namespace: $NAMESPACE
  annotations:
    hypershift.openshift.io/cluster: $NAMESPACE/$cluster_name
$pause_annotations
spec:
  arch: amd64
  clusterName: $cluster_name
$pause_until
  management:
    autoRepair: true
    replace:
      rollingUpdate:
        maxSurge: 1
        maxUnavailable: 0
      strategy: RollingUpdate
    upgradeType: Replace
  nodeDrainTimeout: 0s
  nodeVolumeDetachTimeout: 0s
  platform:
    aws:
      instanceProfile: $infra_id-worker
      instanceType: m6i.large
      rootVolume:
        size: 120
        type: gp3
      subnet:
        id: subnet-$(openssl rand -hex 8)
    type: AWS
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.19.19-x86_64
  replicas: 2
EOF

    local exit_code=$?
    if [[ $exit_code -eq 0 ]]; then
        echo "    ✅ $cluster_name created successfully"
        return 0
    else
        echo "    ❌ Failed to create $cluster_name (exit code: $exit_code)"
        return 1
    fi
}

# Create clusters
echo "🏗️  Creating clusters..."

created_clusters=0
paused_clusters=0

for i in $(seq 1 $NUM_CLUSTERS); do
    echo "🔍 Starting iteration $i of $NUM_CLUSTERS"
    if create_cluster "$i"; then
        created_clusters=$((created_clusters + 1))
        if [[ $((i % 2)) -eq 1 ]]; then
            paused_clusters=$((paused_clusters + 1))
        fi
        echo "✅ Completed iteration $i successfully"
    else
        echo "❌ Failed iteration $i"
    fi
    echo ""
done

echo "🎉 Cluster creation completed!"
echo "   Created: $created_clusters/$NUM_CLUSTERS HostedClusters"
echo "   Paused: $paused_clusters (with OADP annotations)"
echo "   Active: $((created_clusters - paused_clusters))"
echo "   NodePools: $created_clusters"
