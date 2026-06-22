# Create GCP Infrastructure

This guide explains how to create network infrastructure for GCP hosted clusters using the `hypershift create infra gcp` command.

## Prerequisites

- The `hypershift` CLI built from the repository
- `gcloud` CLI authenticated with permissions in the hosted cluster GCP project
- A GCP project for the hosted cluster with required APIs enabled:

```bash
gcloud services enable \
  compute.googleapis.com \
  dns.googleapis.com \
  iam.googleapis.com \
  iamcredentials.googleapis.com \
  cloudresourcemanager.googleapis.com \
  --project=<hosted-cluster-project-id>
```

## Create Infrastructure

The `hypershift create infra gcp` command creates network resources in the hosted cluster project:

- **VPC** — Virtual Private Cloud network for worker nodes
- **Subnet** — Subnet within the VPC
- **Firewall rule** — Allows kubelet access
- **Cloud Router + NAT** — Egress for worker nodes

```bash
hypershift create infra gcp \
  --infra-id=<infra-id> \
  --project-id=<hosted-cluster-project-id> \
  --region=<region>
```

!!! warning "Infra ID constraints"

    The `--infra-id` value must not start with `gcp-` (reserved by GCP for Workload Identity Pool IDs). Use the same `--infra-id` value across all `hypershift create` commands (`infra`, `iam`, `cluster`).

### Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--infra-id` | Yes | Infrastructure ID used for naming GCP resources |
| `--project-id` | Yes | GCP project ID where infrastructure will be created |
| `--region` | Yes | GCP region (e.g., `us-central1`) |
| `--vpc-cidr` | No | CIDR block for the subnet (default: `10.0.0.0/24`) |
| `--output-file` | No | Path to save output JSON with resource names |

### Example

```bash
hypershift create infra gcp \
  --infra-id=my-cluster \
  --project-id=my-hc-project \
  --region=us-central1 \
  > infra-output.json
```

### Output

The command outputs JSON with the created resource names:

```json
{
  "region": "us-central1",
  "projectId": "my-hc-project",
  "infraId": "my-cluster",
  "networkName": "my-cluster-network",
  "subnetName": "my-cluster-subnet",
  "subnetCidr": "10.0.0.0/24",
  "routerName": "my-cluster-router",
  "natName": "my-cluster-nat",
  "firewallRuleName": "my-cluster-allow-kubelet"
}
```

Save this output — you will need the `networkName` and `subnetName` values when creating the hosted cluster.

## Destroy Infrastructure

To clean up infrastructure resources:

```bash
hypershift destroy infra gcp \
  --infra-id=<infra-id> \
  --project-id=<hosted-cluster-project-id> \
  --region=<region>
```

## Next Steps

- [Create GCP IAM Resources](create-gcp-iam.md) — Create WIF pool and service accounts
- [Create a GCP Hosted Cluster](create-gcp-hosted-cluster.md) — Deploy your hosted cluster
