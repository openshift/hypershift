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
  --network=<vpc-name> \
  --subnet=<subnet-name> \
  --private-service-connect-subnet=<psc-subnet> \
  --endpoint-access=PublicAndPrivate \
  --workload-identity-project-number=<project-number> \
  --workload-identity-pool-id=<pool-id> \
  --workload-identity-provider-id=<provider-id> \
  --control-plane-service-account=<controlplane-sa-email> \
  --node-pool-service-account=<nodepool-sa-email> \
  --cloud-controller-service-account=<cloud-controller-sa-email> \
  --storage-service-account=<storage-sa-email> \
  --image-registry-service-account=<image-registry-sa-email> \
  --service-account-signing-key-path=<path-to-sa-signer.key> \
  --oidc-issuer-url=<oidc-issuer-url> \
  --base-domain=<your-dns-domain> \
  --external-dns-domain=<your-dns-domain> \
  --node-pool-replicas=2 \
  --feature-set=TechPreviewNoUpgrade \
  --annotations=hypershift.openshift.io/capi-provider-gcp-image=<capg-image>
```

!!! note "CAPG Image Override (GCP-426)"

    Until HyperShift's CAPI CRDs serve v1beta2, you must pin the CAPG image via the annotation above. Use the CAPG image from the release payload:

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
| `--subnet` | Yes | Subnet name for worker nodes (from `create infra gcp` output) |
| `--private-service-connect-subnet` | Yes | Subnet for PSC endpoints in the HC project (from `create infra gcp` output) |
| `--endpoint-access` | Yes | `Private` or `PublicAndPrivate` |
| `--workload-identity-project-number` | Yes | GCP project number (from `create iam gcp` output) |
| `--workload-identity-pool-id` | Yes | WIF pool ID (from `create iam gcp` output) |
| `--workload-identity-provider-id` | Yes | WIF provider ID (from `create iam gcp` output) |
| `--control-plane-service-account` | Yes | Control Plane Operator SA email |
| `--node-pool-service-account` | Yes | NodePool CAPG SA email |
| `--cloud-controller-service-account` | Yes | Cloud Controller Manager SA email |
| `--storage-service-account` | Yes | GCP PD CSI Driver SA email |
| `--image-registry-service-account` | Yes | Image Registry Operator SA email |
| `--service-account-signing-key-path` | Yes | Path to RSA private key for OIDC token signing |
| `--oidc-issuer-url` | Yes | OIDC issuer URL |
| `--node-pool-replicas` | Yes | Number of worker nodes (default: 0) |
| `--base-domain` | Yes | Base DNS domain for the hosted cluster |
| `--external-dns-domain` | Yes | DNS domain for ExternalDNS-managed hostnames (API server, OAuth) |
| `--feature-set` | Yes | Must be `TechPreviewNoUpgrade` for GCP platform |
| `--machine-type` | No | GCP machine type (default: `n2-standard-4`) |
| `--zone` | No | GCP zone for nodes (default: `{region}-a`) |
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

## Destroy Hosted Cluster

```bash
hypershift destroy cluster gcp \
  --name=<cluster-name> \
  --namespace=<namespace>
```

After the cluster is destroyed, clean up the infrastructure and IAM resources:

```bash
hypershift destroy infra gcp \
  --infra-id=<infra-id> \
  --project-id=<hosted-cluster-project-id> \
  --region=<region>

hypershift destroy iam gcp \
  --infra-id=<infra-id> \
  --project-id=<hosted-cluster-project-id>
```

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
