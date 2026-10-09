# Create a GCP Hosted Cluster

This guide walks through creating a GCP hosted cluster using the infrastructure and IAM resources created in the previous steps.

## Prerequisites

- HyperShift operator installed on a GKE management cluster ([Setup Management Cluster](setup-management-cluster.md))
- Network infrastructure created ([Create GCP Infrastructure](create-gcp-infra.md))
- WIF/IAM resources created ([Create GCP IAM Resources](create-gcp-iam.md))
- An RSA private key for service account token signing (generated during IAM setup)
- A pull secret from [console.redhat.com](https://console.redhat.com/openshift/install/pull-secret)
- An OpenShift release image

## Create Hosted Cluster

```bash
hypershift create cluster gcp \
  --name=<cluster-name> \
  --namespace=<namespace> \
  --release-image=<release-image> \
  --pull-secret=<path-to-pull-secret> \
  --project=<hosted-cluster-project-id> \
  --region=<region> \
  --zone=<zone> \
  --network=<vpc-name> \
  --subnet=<subnet-name> \
  --private-service-connect-subnet=<psc-subnet> \
  --router-name=<router-name> \
  --nat-name=<nat-name> \
  --firewall-rule-name=<firewall-rule-name> \
  --endpoint-access=PublicAndPrivate \
  --workload-identity-project-number=<project-number> \
  --workload-identity-pool-id=<pool-id> \
  --workload-identity-provider-id=<provider-id> \
  --control-plane-service-account=<controlplane-sa-email> \
  --node-pool-service-account=<nodepool-sa-email> \
  --cloud-controller-service-account=<cloud-controller-sa-email> \
  --storage-service-account=<storage-sa-email> \
  --image-registry-service-account=<image-registry-sa-email> \
  --network-service-account=<network-sa-email> \
  --service-account-signing-key-path=<path-to-sa-signer.key> \
  --oidc-issuer-url=<oidc-issuer-url> \
  --base-domain=<your-dns-domain> \
  --external-dns-domain=<your-dns-domain> \
  --node-pool-replicas=2 \
  --feature-set=TechPreviewNoUpgrade \
  --annotations=hypershift.openshift.io/pod-security-admission-label-override=baseline
```

!!! note "Pod Security Admission Override Required"

    GKE clusters enforce pod security policies that block some HyperShift control plane components from starting without the `baseline` override.

!!! warning "CAPG Image Override Required for 4.22.x/4.23.x only ([GCP-841](https://redhat.atlassian.net/browse/GCP-841))"

    The CAPG image in these releases crashes due to a removed `ClusterResourceSet` feature gate. Add the CAPG image annotation:

    ```bash
    --annotations="hypershift.openshift.io/pod-security-admission-label-override=baseline,hypershift.openshift.io/capi-provider-gcp-image=<capg-image>"
    ```

    Get the CAPG image from the release payload:

    ```bash
    oc adm release info <release-image> --image-for=cluster-api-provider-gcp
    ```

### Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--name` | Yes | Name for the hosted cluster |
| `--namespace` | Yes | Namespace for the HostedCluster resource |
| `--release-image` | Yes | OpenShift release image |
| `--pull-secret` | Yes | Path to pull secret file |
| `--project` | Yes | Hosted cluster GCP project ID |
| `--region` | Yes | GCP region |
| `--network` | Yes | VPC network name (from `create infra gcp` output) |
| `--subnet` | Yes | Subnet for worker nodes (from `create infra gcp` output: `subnetName`) |
| `--private-service-connect-subnet` | Yes | Subnet for PSC endpoints (same as `--subnet`) |
| `--router-name` | Yes | Cloud Router name (from `create infra gcp` output: `routerName`) |
| `--nat-name` | Yes | Cloud NAT name (from `create infra gcp` output: `natName`) |
| `--firewall-rule-name` | Yes | Firewall rule name (from `create infra gcp` output: `firewallRuleName`) |
| `--endpoint-access` | Yes | `Private` or `PublicAndPrivate` |
| `--workload-identity-project-number` | Yes | GCP project number (from `create iam gcp` output) |
| `--workload-identity-pool-id` | Yes | WIF pool ID (from `create iam gcp` output) |
| `--workload-identity-provider-id` | Yes | WIF provider ID (from `create iam gcp` output) |
| `--control-plane-service-account` | Yes | Control Plane Operator SA email |
| `--node-pool-service-account` | Yes | NodePool CAPG SA email |
| `--cloud-controller-service-account` | Yes | Cloud Controller Manager SA email |
| `--storage-service-account` | Yes | GCP PD CSI Driver SA email |
| `--image-registry-service-account` | Yes | Image Registry Operator SA email |
| `--network-service-account` | Yes | Cloud Network Config Controller SA email |
| `--service-account-signing-key-path` | Yes | Path to RSA private key for OIDC token signing |
| `--oidc-issuer-url` | Yes | OIDC issuer URL |
| `--node-pool-replicas` | Yes | Number of worker nodes (default: 0) |
| `--base-domain` | Yes | Base DNS domain for the hosted cluster |
| `--external-dns-domain` | Yes | DNS domain for ExternalDNS-managed hostnames (API server, OAuth) |
| `--feature-set` | Yes | Must be `TechPreviewNoUpgrade` for GCP platform |
| `--machine-type` | No | GCP machine type (default: `n2-standard-4`) |
| `--zone` | Conditional | GCP zone for nodes. Required when `--node-pool-replicas >= 0` (default). Optional when `--node-pool-replicas=-1` (no NodePool created). |
| `--boot-image` | No | Override RHCOS boot image from release payload |

## Monitor Cluster Creation

Watch the hosted cluster status:

```bash
oc get hostedcluster -n <namespace> <cluster-name> -w
```

Wait for the `Available` condition to be `True`:

```bash
oc wait --for=condition=Available hostedcluster/<cluster-name> -n <namespace> --timeout=30m
```

## Access the Hosted Cluster

Retrieve the kubeconfig:

```bash
oc get secret <cluster-name>-admin-kubeconfig -n <namespace> -o jsonpath='{.data.kubeconfig}' | base64 -d > hosted-kubeconfig
```

Verify access:

```bash
KUBECONFIG=hosted-kubeconfig oc get nodes
KUBECONFIG=hosted-kubeconfig oc get clusterversion
```

## Image Registry

GCP hosted clusters automatically configure the OpenShift image registry using Workload Identity Federation (WIF). The `--image-registry-service-account` flag passed at cluster creation supplies the GCP service account (GSA) that the registry operator uses to access GCS.

The flow is:

1. The HyperShift control plane generates a WIF credential for the `image-registry` GSA and writes it to the `installer-cloud-credentials` secret in the `openshift-image-registry` namespace.
2. The cluster image registry operator reads the credential and creates a GCS bucket in the hosted cluster project.
3. The registry becomes available at `image-registry.openshift-image-registry.svc:5000`.

### Verify Registry Status

```bash
KUBECONFIG=hosted-kubeconfig oc get clusteroperator image-registry
```

The `AVAILABLE` column should be `True` within a few minutes of nodes joining.

Inspect the GCS bucket chosen by the registry operator:

```bash
KUBECONFIG=hosted-kubeconfig oc get configs.imageregistry.operator.openshift.io cluster \
  -o jsonpath='{.spec.storage}'
```

### Disable the Image Registry

Add `--capabilities-disabled=ImageRegistry` to the `hypershift create cluster gcp` command to skip deploying the registry operator and suppress GCS bucket creation.

To disable the registry on a running cluster, patch the `HostedCluster` resource:

```bash
oc patch hostedcluster <cluster-name> -n <namespace> \
  --type=merge \
  --patch='{"spec":{"capabilities":{"disabled":["ImageRegistry"]}}}'
```

For advanced scenarios (custom bucket, pre-existing bucket, troubleshooting WIF auth), see [Configure Image Registry on GCP](configure-image-registry.md).

## Networking Cleanup Metadata

The required `--router-name`, `--nat-name`, and `--firewall-rule-name` creation flags ensure networking references are available for automatic cleanup. The CLI records their exact names in a versioned HostedCluster annotation:

```yaml
metadata:
  annotations:
    hypershift.openshift.io/gcp-infra-resources: '{"version":1,"router":"example-router","nat":"example-nat","firewallRule":"example-allow-kubelet"}'
```

Project, region, network, subnet, and IAM references continue to come from the existing HostedCluster spec. No additional CRD fields or operator changes are required. Creation commands and CI scripts must supply all three cleanup names from the infrastructure output. HostedClusters created by older CLIs without this annotation require `--preserve-infra` and a separate `destroy infra gcp` command.

Destroy validates the annotation before starting cluster deletion. Missing, incomplete, invalid, or unsupported metadata blocks infrastructure cleanup unless `--preserve-infra` is set. These annotations are mutable: keep the recorded names accurate and only reference infrastructure dedicated to this cluster. The CLI trusts the recorded references without querying GCP to validate their relationships. It captures the names before deletion and never derives them from the HostedCluster InfraID.

## Destroy Hosted Cluster

```bash
hypershift destroy cluster gcp \
  --name=<cluster-name> \
  --namespace=<namespace>
```

When the HostedCluster has the networking cleanup annotation recorded at creation, `hypershift destroy cluster gcp` removes its IAM and network resources as part of cluster destruction. Networking, service accounts, and project role bindings use `spec.platform.gcp.project`. The Workload Identity pool and provider use the existing `spec.platform.gcp.workloadIdentity.projectNumber`, which can identify a different project. The CLI requires GCP credentials with cleanup permissions in the relevant projects. Missing or invalid workload identity project numbers block IAM cleanup before cluster deletion unless `--preserve-iam` is set. For HostedClusters without the networking annotation, preserve infrastructure during cluster deletion and clean it up using the original infrastructure InfraID:

```bash
hypershift destroy cluster gcp \
  --name=<cluster-name> \
  --namespace=<namespace> \
  --preserve-infra

hypershift destroy infra gcp \
  --infra-id=<original-infra-id> \
  --project-id=<hosted-cluster-project-id> \
  --region=<region>
```

If an older HostedCluster is also missing its workload identity references or service account emails, add `--preserve-iam` and use `hypershift destroy iam gcp --infra-id=<original-iam-infra-id> --project-id=<original-iam-project-id>` after cluster deletion. The standalone cleanup commands use the InfraID and project from their corresponding `create infra` or `create iam` command. If the pool/provider and service accounts were provisioned in separate projects outside `create iam gcp`, clean them up separately in their respective projects; the standalone IAM command assumes its resources share one project.

## Troubleshooting

### Check Hosted Control Plane Pods

```bash
oc get pods -n <namespace>-<cluster-name>
```

### Check HostedCluster Conditions

```bash
oc get hostedcluster -n <namespace> <cluster-name> -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.message}{"\n"}{end}'
```

### Check NodePool Status

```bash
oc get nodepool -n <namespace> -o yaml
```

### Common Issues

- **WIF validation fails** — Ensure all service account emails match the output from `create iam gcp`
- **PSC endpoint not available** — Verify the operator has WIF credentials and the PSC subnet exists
- **Nodes not joining** — Check that the boot image is available and the hosted cluster project has compute API enabled
- **Image registry operator not available** — Confirm the `installer-cloud-credentials` secret exists in `openshift-image-registry` and the WIF credential references the correct pool/provider; see [Configure Image Registry on GCP](configure-image-registry.md) for details
- **GCS bucket creation fails (403)** — The `image-registry` GSA is missing `roles/storage.admin`; grant it with `gcloud projects add-iam-policy-binding` or re-run `hypershift create iam gcp`
