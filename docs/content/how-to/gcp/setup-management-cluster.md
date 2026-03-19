# Setup GCP Management Cluster

This guide walks through installing the HyperShift operator on a GKE Autopilot cluster with GCP platform support.

## Prerequisites

- A GCP project for the management cluster
- `gcloud` CLI installed and authenticated
- `kubectl` or `oc` configured to access the GKE cluster
- The `hypershift` CLI built from the repository
- A pull secret from [console.redhat.com](https://console.redhat.com/openshift/install/pull-secret)

## Enable Required APIs

Enable the GCP APIs needed for the management cluster:

```bash
gcloud services enable \
  container.googleapis.com \
  compute.googleapis.com \
  dns.googleapis.com \
  cloudresourcemanager.googleapis.com \
  --project=<control-plane-project-id>
```

## Create GKE Autopilot Cluster

If you don't already have a GKE cluster, create one:

```bash
gcloud container clusters create-auto <cluster-name> \
  --project=<control-plane-project-id> \
  --region=<region> \
  --release-channel=stable \
  --quiet
```

Configure `kubectl` to use the new cluster:

```bash
gcloud container clusters get-credentials <cluster-name> \
  --project=<control-plane-project-id> \
  --region=<region>
```

## Create PSC Subnet

Private Service Connect (PSC) provides private connectivity between the hosted cluster worker nodes and the control plane API server. Create a PSC subnet in the same VPC as the GKE cluster:

```bash
# Get the VPC name used by the GKE cluster
VPC_NAME=$(gcloud container clusters describe <cluster-name> \
  --project=<control-plane-project-id> \
  --region=<region> \
  --format='value(networkConfig.network)' | xargs basename)

# Create PSC subnet
gcloud compute networks subnets create <infra-id>-psc \
  --project=<control-plane-project-id> \
  --region=<region> \
  --network="${VPC_NAME}" \
  --range=10.3.0.0/24 \
  --purpose=PRIVATE_SERVICE_CONNECT \
  --quiet
```

Note the PSC subnet name — you will need it when creating the hosted cluster.

## DNS Zone Configuration

Before creating HostedClusters, you need to set up a Cloud DNS zone for ExternalDNS to manage API server and OAuth endpoint DNS records.

You can either use an existing DNS zone in a shared project, or create a new one for testing.

### Create a Cloud DNS Zone

```bash
DNS_PROJECT_ID=<dns-project-id>
DNS_ZONE_NAME=<zone-name>
DNS_DOMAIN=<your-dns-domain>

# Enable DNS API if not already enabled
gcloud services enable dns.googleapis.com --project="${DNS_PROJECT_ID}"

# Create the DNS zone
gcloud dns managed-zones create "${DNS_ZONE_NAME}" \
  --project="${DNS_PROJECT_ID}" \
  --dns-name="${DNS_DOMAIN}." \
  --description="DNS zone for HyperShift hosted clusters" \
  --visibility=public \
  --quiet
```

!!! tip "Same Project for Dev/Test"

    For development or testing, you can create the DNS zone in the same project as the management cluster (`DNS_PROJECT_ID=<control-plane-project-id>`). This avoids cross-project IAM configuration for ExternalDNS.

### Delegate DNS from Parent Zone (Optional)

If your DNS domain is a subdomain of an existing zone, delegate it by adding NS records to the parent zone:

```bash
PARENT_DNS_PROJECT=<parent-dns-project-id>
PARENT_DNS_ZONE=<parent-zone-name>
SUBDOMAIN_NAME=<subdomain>

# Get name servers from your new zone
NS_SERVERS=$(gcloud dns managed-zones describe "${DNS_ZONE_NAME}" \
  --project="${DNS_PROJECT_ID}" \
  --format="value(nameServers)" | tr ';' '\n')

# Add NS records to parent zone
for ns in ${NS_SERVERS}; do
  gcloud dns record-sets transaction start \
    --zone="${PARENT_DNS_ZONE}" \
    --project="${PARENT_DNS_PROJECT}" 2>/dev/null || true
  gcloud dns record-sets transaction add "${ns}" \
    --zone="${PARENT_DNS_ZONE}" \
    --project="${PARENT_DNS_PROJECT}" \
    --name="${SUBDOMAIN_NAME}.${PARENT_DNS_DOMAIN}." \
    --type=NS \
    --ttl=300
  gcloud dns record-sets transaction execute \
    --zone="${PARENT_DNS_ZONE}" \
    --project="${PARENT_DNS_PROJECT}"
done
```

### Create ExternalDNS Service Account

Create a GCP service account for ExternalDNS with DNS admin permissions:

```bash
gcloud iam service-accounts create external-dns \
  --project="${DNS_PROJECT_ID}" \
  --display-name="ExternalDNS Service Account"

gcloud projects add-iam-policy-binding "${DNS_PROJECT_ID}" \
  --member="serviceAccount:external-dns@${DNS_PROJECT_ID}.iam.gserviceaccount.com" \
  --role="roles/dns.admin" \
  --quiet
```

Note the DNS project ID, DNS domain, and ExternalDNS service account email — you will need them when installing the operator and configuring ExternalDNS WIF.

## Install Required CRDs

GKE does not include OpenShift CRDs. Install the CRDs that the HyperShift operator expects:

```bash
# Prometheus operator CRDs
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml

# OpenShift Route CRD
oc apply -f https://raw.githubusercontent.com/openshift/api/6bababe9164ea6c78274fd79c94a3f951f8d5ab2/route/v1/zz_generated.crd-manifests/routes.crd.yaml

# DNSEndpoint CRD (for ExternalDNS)
oc apply -f https://raw.githubusercontent.com/kubernetes-sigs/external-dns/v0.15.0/docs/contributing/crd-source/crd-manifest.yaml
```

## Install HyperShift Operator

Install the operator with GCP platform support:

```bash
hypershift install \
  --tech-preview-no-upgrade \
  --enable-conversion-webhook=false \
  --external-dns-provider=google \
  --external-dns-domain-filter=<your-dns-domain> \
  --external-dns-google-project=<dns-project-id> \
  --private-platform=GCP \
  --gcp-project=<control-plane-project-id> \
  --gcp-region=<region> \
  --pull-secret=<path-to-pull-secret> \
  --limit-crd-install=GCP \
  --wait-until-available
```

!!! tip "Custom HyperShift Image"

    Add `--hypershift-image quay.io/hypershift/hypershift:TAG` if using a custom operator image.

## Configure Operator Workload Identity

The HyperShift operator needs a GCP service account with PSC permissions to manage Private Service Connect resources.

### Create GCP Service Account

```bash
CP_PROJECT_ID=<control-plane-project-id>

gcloud iam service-accounts create hypershift-operator \
  --project="${CP_PROJECT_ID}" \
  --display-name="HyperShift Operator"
```

### Create Custom IAM Role

Create a role with minimal PSC permissions:

```bash
gcloud iam roles create hypershiftPSCOperator \
  --project="${CP_PROJECT_ID}" \
  --title="HyperShift PSC Operator" \
  --permissions=compute.forwardingRules.list,compute.forwardingRules.use,compute.serviceAttachments.create,compute.serviceAttachments.delete,compute.serviceAttachments.get,compute.serviceAttachments.list,compute.subnetworks.list,compute.subnetworks.use,compute.regionOperations.get
```

### Bind Role and Configure WIF

```bash
# Bind the role to the service account
gcloud projects add-iam-policy-binding "${CP_PROJECT_ID}" \
  --member="serviceAccount:hypershift-operator@${CP_PROJECT_ID}.iam.gserviceaccount.com" \
  --role="projects/${CP_PROJECT_ID}/roles/hypershiftPSCOperator"

# Configure Workload Identity binding
gcloud iam service-accounts add-iam-policy-binding \
  "hypershift-operator@${CP_PROJECT_ID}.iam.gserviceaccount.com" \
  --project="${CP_PROJECT_ID}" \
  --member="serviceAccount:${CP_PROJECT_ID}.svc.id.goog[hypershift/operator]" \
  --role="roles/iam.workloadIdentityUser" \
  --condition=None \
  --quiet

# Annotate the Kubernetes service account
oc annotate serviceaccount operator -n hypershift \
  iam.gke.io/gcp-service-account=hypershift-operator@${CP_PROJECT_ID}.iam.gserviceaccount.com \
  --overwrite

# Restart the operator to pick up WIF credentials
oc rollout restart deployment operator -n hypershift
oc rollout status deployment operator -n hypershift --timeout=120s
```

## Configure ExternalDNS Workload Identity

ExternalDNS manages DNS records for hosted cluster API endpoints. It needs WIF access to impersonate the ExternalDNS GCP service account created in the [DNS Zone Configuration](#dns-zone-configuration) section.

```bash
DNS_PROJECT_ID=<dns-project-id>
EXTERNAL_DNS_SA=external-dns@${DNS_PROJECT_ID}.iam.gserviceaccount.com

# Allow ExternalDNS K8s SA to impersonate the DNS service account
# Cross-project WIF requires both workloadIdentityUser and serviceAccountTokenCreator
gcloud iam service-accounts add-iam-policy-binding "${EXTERNAL_DNS_SA}" \
  --role=roles/iam.workloadIdentityUser \
  --member="serviceAccount:${CP_PROJECT_ID}.svc.id.goog[hypershift/external-dns]" \
  --project="${DNS_PROJECT_ID}" \
  --condition=None \
  --quiet

gcloud iam service-accounts add-iam-policy-binding "${EXTERNAL_DNS_SA}" \
  --role=roles/iam.serviceAccountTokenCreator \
  --member="serviceAccount:${CP_PROJECT_ID}.svc.id.goog[hypershift/external-dns]" \
  --project="${DNS_PROJECT_ID}" \
  --condition=None \
  --quiet

# Annotate ExternalDNS K8s SA and restart
oc annotate serviceaccount external-dns -n hypershift \
  iam.gke.io/gcp-service-account=${EXTERNAL_DNS_SA} \
  --overwrite

oc rollout restart deployment/external-dns -n hypershift
oc rollout status deployment/external-dns -n hypershift --timeout=120s
```

## Verification

Verify the operator and ExternalDNS are running:

```bash
oc get deployment -n hypershift
oc get pods -n hypershift
```

## Next Steps

- [Create GCP Infrastructure](create-gcp-infra.md) — Create VPC and subnet for hosted clusters
- [Create GCP IAM Resources](create-gcp-iam.md) — Create WIF pool and service accounts
