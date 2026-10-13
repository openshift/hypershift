# GCP

This section provides guides for deploying HyperShift hosted clusters on Google Cloud Platform. GCP uses a GKE Autopilot cluster as the management platform and Workload Identity Federation (WIF) for tokenless authentication.

!!! note "TechPreview in OCP 4.22"

    GCP HostedClusters are available as a TechPreview feature in OpenShift Container Platform 4.22.

## Deployment Model

GCP hosted clusters use a **two-project model** that mirrors the production architecture:

| Component | GCP Project | Purpose |
|-----------|-------------|---------|
| **Management Cluster** | Control Plane project | GKE Autopilot cluster running the HyperShift operator and hosted control planes |
| **Hosted Cluster** | Hosted Cluster project | Worker nodes, WIF pool/provider, service accounts, VPC/subnet |

**Key technologies:**

- **GKE Autopilot** — Managed Kubernetes for the management cluster
- **Workload Identity Federation (WIF)** — Tokenless authentication between Kubernetes service accounts and GCP service accounts
- **Private Service Connect (PSC)** — Private connectivity between worker nodes and the hosted control plane API server

## Guides

- [Setup Management Cluster](setup-management-cluster.md) — Install HyperShift operator on GKE with GCP support
- [Create GCP Infrastructure](create-gcp-infra.md) — Create network infrastructure (VPC, subnet)
- [Create GCP IAM Resources](create-gcp-iam.md) — Create WIF pool, OIDC provider, and service accounts
- [Create a GCP Hosted Cluster](create-gcp-hosted-cluster.md) — Deploy your first hosted cluster
- [E2E GKE CI Job](e2e-gke-ci-job.md) — CI job for validating GCP platform changes

## Prerequisites

Before getting started, you need:

- A GCP project for the management cluster (control plane)
- A GCP project for the hosted cluster (worker nodes and WIF)
- The `gcloud` CLI installed and authenticated
- The `hypershift` CLI built from the repository
- A GCP service account with project-level permissions or appropriate roles
- A DNS zone for hosted cluster endpoints (for ExternalDNS)

## Additional Resources

- [GCP Workload Identity Federation](https://cloud.google.com/iam/docs/workload-identity-federation)
- [GKE Autopilot](https://cloud.google.com/kubernetes-engine/docs/concepts/autopilot-overview)
- [Private Service Connect](https://cloud.google.com/vpc/docs/private-service-connect)
