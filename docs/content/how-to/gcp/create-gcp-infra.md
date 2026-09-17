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
- **Cloud Router + NAT** — Egress for worker nodes

!!! note "Worker firewall rule"

    The worker firewall rule (`<infra-id>-internal-cluster`) is **not** created by
    this command. It is created and continuously reconciled by the
    control-plane-operator (CPO) throughout the cluster lifecycle and torn down on
    deletion. See [worker firewall rules](#worker-firewall-rules) below.

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
  "natName": "my-cluster-nat"
}
```

Save this output — you will need the `networkName` and `subnetName` values when creating the hosted cluster.

## Worker firewall rules

The control-plane-operator (CPO) owns the worker firewall rule
`<infra-id>-internal-cluster`. It is created in the hosted cluster's GCP project
and VPC, continuously reconciled while the cluster exists, and deleted during
hosted cluster teardown (before this CLI deletes the VPC). This CLI never creates
or deletes the firewall rule.

The managed rule is an enabled `INGRESS` / `ALLOW` rule at priority `1000`,
scoped by the `<infra-id>-worker` network tag as **both** source and target
(no machine, pod, or PSC CIDR allowances):

| Protocol | Ports |
|----------|-------|
| TCP | `10250`, `9000-9999`, `30000-32767` |
| UDP | `9000-9999`, `30000-32767`; plus `6081` for `OVNKubernetes` (Geneve overlay) |

CPO uses its mounted Workload Identity Federation credentials to reconcile the
rule and requires the `roles/compute.securityAdmin` role on the `ctrlplane-op`
service account (provisioned by `hypershift create iam gcp`). The reconcile is
best-effort: if credentials are not yet available or the IAM role is missing, the
`GCPFirewallRulesReady` condition on the HostedCluster/HostedControlPlane reports
`False` with an actionable message and converges automatically once the
prerequisite is satisfied — cluster bootstrap is never blocked.

### Enabling on an existing cluster

Because the reconciler is name-based and idempotent, an existing cluster whose
CPO is on a release payload containing this feature converges the same as a fresh
one. The only prerequisite is granting `roles/compute.securityAdmin` to
`ctrlplane-op` — re-run `hypershift create iam gcp` (it grants the role to the
already-created service account; no cluster recreation is needed). Until the role
is granted, `GCPFirewallRulesReady` reports `False` with a permission-denied
message.

## Destroy Infrastructure

To clean up infrastructure resources:

!!! warning "Delete the hosted cluster first"

    Destroy the **hosted cluster** before running `hypershift destroy infra gcp`.
    The CPO-managed worker firewall rule is torn down during hosted cluster
    deletion; deleting the VPC while the rule still exists will fail. If cloud
    resource cleanup was skipped (via the cleanup opt-out annotation), the managed
    firewall rule may remain and must be removed manually before deleting the VPC.

```bash
hypershift destroy infra gcp \
  --infra-id=<infra-id> \
  --project-id=<hosted-cluster-project-id> \
  --region=<region>
```

## Next Steps

- [Create GCP IAM Resources](create-gcp-iam.md) — Create WIF pool and service accounts
- [Create a GCP Hosted Cluster](create-gcp-hosted-cluster.md) — Deploy your hosted cluster
