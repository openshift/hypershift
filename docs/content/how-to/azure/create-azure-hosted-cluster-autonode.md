---
title: Create AutoNode hosted clusters
---

HyperShift AutoNode uses Karpenter to provision worker VMs for unschedulable workloads on self-managed Azure HostedClusters. Karpenter runs in the hosted control plane namespace on the management cluster and watches NodePools in the hosted cluster.

## Prerequisites

- An OpenShift management cluster prepared following [Setup Azure Management Cluster for HyperShift](setup-management-cluster.md).
- An Azure OIDC issuer and matching service account signing key configured following [Configure OIDC Issuer](create-iam-separately.md#configure-oidc-issuer).
- The HyperShift CLI and OpenShift CLI (`oc`).
- An Azure credentials file with permission to create managed identities, provision infrastructure, and assign Azure roles. See [Create Azure IAM Resources](create-iam-separately.md#prerequisites).
- A valid OpenShift pull secret and a release image containing the `karpenter-operator` and `azure-karpenter-provider-azure` components.
- Azure quota for the worker VMs. The example uses on-demand `Standard_D4s_v5` VMs.

!!! warning "Customize the example values"

    Customize the example values below for your environment before running the commands. Use the OIDC issuer URL and matching service account signing key configured in the prerequisites.

```bash
export CLUSTER_NAME="my-autonode-cluster"
export INFRA_ID="${CLUSTER_NAME}-$(openssl rand -hex 4)"
export LOCATION="eastus"
export BASE_DOMAIN="example.com"
export AZURE_CREDS="/path/to/azure-creds.json"
export PULL_SECRET="/path/to/pull-secret.json"
export RELEASE_IMAGE="<release-image>"
export PERSISTENT_RG_NAME="os4-common"
export OIDC_ISSUER_URL="https://yourstorageaccount.blob.core.windows.net/yourstorageaccount"
export SA_TOKEN_ISSUER_PRIVATE_KEY_PATH="/path/to/serviceaccount-signer.private"
```

Commands use the management cluster kubeconfig unless `--kubeconfig` is specified.

## Create Workload Identities for AutoNode

Create workload identities with `--enable-karpenter` to include Karpenter's identity:

```bash
hypershift create iam azure \
    --name "${CLUSTER_NAME}" \
    --infra-id "${INFRA_ID}" \
    --azure-creds "${AZURE_CREDS}" \
    --location "${LOCATION}" \
    --resource-group-name "${PERSISTENT_RG_NAME}" \
    --oidc-issuer-url "${OIDC_ISSUER_URL}" \
    --output-file workload-identities.json \
    --enable-karpenter
```

If you are reusing IAM resources, rerun IAM creation with `--enable-karpenter` and your original options. Regenerate any existing infrastructure output from the updated IAM file before using `--infra-json`.

## Install the HyperShift Operator with AutoNode Support

Azure AutoNode requires the standalone Karpenter operator and the [management cluster `TechPreviewNoUpgrade` feature set](../feature-gates.md):

```bash
hypershift install \
    --pull-secret "${PULL_SECRET}" \
    --limit-crd-install Azure \
    --tech-preview-no-upgrade \
    --enable-standalone-karpenter-operator
```

If your management cluster uses External DNS or Azure Private Link, keep the corresponding installation options from [Setup Azure Management Cluster for HyperShift](setup-management-cluster.md) and [Deploy Azure Private Clusters](deploy-azure-private-clusters.md).

## Create the HostedCluster with AutoNode

### Create Infrastructure and Assign Roles

Pass the IAM output to infrastructure creation and enable role assignment:

```bash
hypershift create infra azure \
    --name "${CLUSTER_NAME}" \
    --infra-id "${INFRA_ID}" \
    --azure-creds "${AZURE_CREDS}" \
    --base-domain "${BASE_DOMAIN}" \
    --location "${LOCATION}" \
    --workload-identities-file workload-identities.json \
    --assign-identity-roles \
    --dns-zone-rg-name "${PERSISTENT_RG_NAME}" \
    --output-file infra-output.yaml
```

For role scopes, see [Self-Managed Azure Infrastructure](../../reference/infrastructure/azure-self-managed.md#role-assignment-scopes). For existing network resources, see [Create Azure Infrastructure Separately](create-infra-separately.md).

### Create the HostedCluster

Enable AutoNode with `--auto-node`:

```bash
hypershift create cluster azure \
    --name "${CLUSTER_NAME}" \
    --infra-id "${INFRA_ID}" \
    --azure-creds "${AZURE_CREDS}" \
    --location "${LOCATION}" \
    --node-pool-replicas 2 \
    --base-domain "${BASE_DOMAIN}" \
    --pull-secret "${PULL_SECRET}" \
    --generate-ssh \
    --release-image "${RELEASE_IMAGE}" \
    --sa-token-issuer-private-key-path "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}" \
    --oidc-issuer-url "${OIDC_ISSUER_URL}" \
    --infra-json infra-output.yaml \
    --diagnostics-storage-account-type Managed \
    --auto-node
```

Do not combine `--assign-service-principal-roles` with `--infra-json`. Roles were assigned during infrastructure creation.

For External DNS, also pass `--external-dns-domain <your-dns-zone-name>`. For private endpoint access, follow [Deploy Azure Private Clusters](deploy-azure-private-clusters.md).

The two initial workers belong to a HyperShift NodePool managed by Cluster API Provider Azure. AutoNode provisions additional workers according to the Karpenter NodePools you create in the hosted cluster.

Wait for the hosted cluster API and AutoNode components before configuring Karpenter:

```bash
oc wait --for=condition=Available hostedcluster/"${CLUSTER_NAME}" -n clusters --timeout=30m
oc wait --for=condition=AutoNodeEnabled hostedcluster/"${CLUSTER_NAME}" -n clusters --timeout=30m
```

Extract the hosted cluster kubeconfig:

```bash
export HOSTED_CLUSTER_KUBECONFIG="${PWD}/${CLUSTER_NAME}-kubeconfig"
hypershift create kubeconfig \
    --name "${CLUSTER_NAME}" \
    --namespace clusters > "${HOSTED_CLUSTER_KUBECONFIG}"
```

## Configure Karpenter in the Hosted Cluster

The platform creates an `OpenShiftAzureNodeClass` named `default` in the hosted cluster for use by Karpenter NodePools. Do not modify or delete this platform-managed default NodeClass. The platform reverts changes to its specification and recreates it if deleted.

Inspect the default NodeClass and its platform-populated settings:

```bash
oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" get openshiftazurenodeclasses.karpenter.hypershift.openshift.io default -o yaml
```

### NodeClass Fields

For custom node settings, create an additional `OpenShiftAzureNodeClass`. All fields below are optional and belong to `spec`. Some effective defaults are applied during reconciliation rather than stored in the NodeClass specification.

| Field | Description | Default when omitted |
|-------|-------------|----------------------|
| `vnetSubnetID` | Azure resource ID of the subnet to which the nodes' network interfaces attach. The subnet must be in the cluster's virtual network. | The hosted cluster's subnet. This value is fixed on the platform-managed default NodeClass. |
| `osDiskSizeGiB` | Size of the nodes' operating system disk in GiB, from 30 to 2048. | 120 GiB. |
| `tags` | Azure tags applied to VMs and network interfaces. Up to 47 user-defined tags can be specified. Platform-reserved tag names cannot be used. | No user-defined tags. Platform tags still apply. |
| `kubelet` | Kubelet settings delivered through ignition, including pod limits, resource reservations, and eviction thresholds. On Azure, `maxPods` must be between 10 and 250. | OpenShift kubelet defaults. |
| `security.encryption.hostEncryption` | Host-based encryption for temporary disks and disk caches. Accepts `Enabled` or `Disabled`. `Enabled` requires the Azure subscription's `EncryptionAtHost` feature and compatible VM sizes. | `Disabled`. |
| `version` | Pins nodes to an OpenShift version. Changing this value replaces the nodes. | When `version` is omitted, nodes created from this NodeClass follow the last completed control plane version. They are replaced after each control plane upgrade completes. |

The OS disk size and host encryption defaults are platform-selected and may change between releases. For the full kubelet field set, see [`KubeletConfiguration`](https://github.com/openshift/karpenter-operator/blob/main/api/karpenter/v1/kubelet_config.go).

`version: ""` is invalid. To follow the control plane, omit `spec.version`. An explicit `version` must be a semantic version in the cluster's update channel. It cannot be a newer minor version than the control plane or an older minor version than the NodeClass's `status.version`. Supported skew is up to three minor versions behind the control plane. The `SupportedVersionSkew` condition reports whether the version is within that range without changing readiness.

### Create a Karpenter NodePool

!!! note "NodeClass References"

    `AKSNodeClass` is an operator-managed implementation detail, not a user-facing configuration API. Do not edit or create `AKSNodeClass` resources directly. In a Karpenter NodePool, set `nodeClassRef` to the generated `AKSNodeClass` with the same name as your `OpenShiftAzureNodeClass`.

Create a Karpenter NodePool for on-demand Azure VMs:

```bash
cat <<'EOF' | oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" apply -f -
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: azure-autonode
spec:
  limits:
    cpu: "16"
  disruption:
    consolidationPolicy: WhenEmptyOrUnderutilized
    consolidateAfter: 60s
  template:
    spec:
      nodeClassRef:
        group: karpenter.azure.com
        kind: AKSNodeClass
        name: default
      requirements:
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: kubernetes.io/os
          operator: In
          values: ["linux"]
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand"]
        - key: node.kubernetes.io/instance-type
          operator: In
          values: ["Standard_D4s_v5"]
EOF
```

## Deploy a Sample Workload

The sample workload selects the `azure-autonode` Karpenter NodePool so its pods cannot run on the initial HyperShift NodePool workers:

```bash
cat <<'EOF' | oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: auto-node-example
spec:
  replicas: 0
  selector:
    matchLabels:
      app: auto-node-example
  template:
    metadata:
      labels:
        app: auto-node-example
    spec:
      nodeSelector:
        karpenter.sh/nodepool: azure-autonode
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: workload
          image: registry.access.redhat.com/ubi10/ubi-minimal:10.1
          command: ["sleep", "infinity"]
          resources:
            requests:
              cpu: "1"
              memory: 256Mi
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
EOF
```

Scale the workload to create demand for Karpenter workers:

```bash
oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" scale deployment/auto-node-example --replicas=3
```

## Verify Node Provisioning

Inspect the Karpenter NodePool and watch NodeClaims in the hosted cluster:

```bash
oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" get nodepools.karpenter.sh azure-autonode
oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" get nodeclaims.karpenter.sh --watch
```

Stop the watch with `Ctrl+C`. To inspect a NodeClaim's conditions and events:

```bash
oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" describe nodeclaims.karpenter.sh <nodeclaim-name>
```

Check that workers have joined and the sample pods are scheduled on them:

```bash
oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" get nodes -l karpenter.sh/nodepool=azure-autonode
oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" get pods -l app=auto-node-example -o wide
```

## Cleanup

Remove the sample workload and its Karpenter NodePool:

```bash
oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" delete deployment auto-node-example
oc --kubeconfig "${HOSTED_CLUSTER_KUBECONFIG}" delete nodepools.karpenter.sh azure-autonode
```

Wait for Karpenter to delete its Azure resources before [destroying the HostedCluster and infrastructure](create-self-managed-azure-cluster.md#cleanup).

!!! note "Resource Cleanup"

    The HyperShift destroy commands clean up the cluster and infrastructure resources. Workload identities, including the Karpenter identity, and the OIDC issuer created during setup can be reused for other clusters. Clean them up separately after cluster cleanup if no longer needed. See [Destroying Workload Identities](create-iam-separately.md#destroying-workload-identities).

## Related Documentation

- [Self-Managed Azure Infrastructure](../../reference/infrastructure/azure-self-managed.md)
- [Create Azure IAM Resources](create-iam-separately.md)
- [Autoscaling for Self-Managed Azure HostedClusters](autoscaling-self-managed.md)
- [Karpenter NodePools](https://karpenter.sh/docs/concepts/nodepools/)
