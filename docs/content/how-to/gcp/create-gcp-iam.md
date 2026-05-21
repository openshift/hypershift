# Create GCP IAM Resources

This guide explains how to create Workload Identity Federation (WIF) resources for GCP hosted clusters using the `hypershift create iam gcp` command.

## Prerequisites

- The `hypershift` CLI built from the repository
- `gcloud` CLI authenticated with IAM permissions in the hosted cluster GCP project
- An RSA keypair for OIDC service account token signing

## Generate RSA Keypair

The hosted cluster's OIDC provider requires an RSA keypair for signing service account tokens:

```bash
# Generate 4096-bit RSA key in PKCS#1 format
openssl genrsa -traditional -out sa-signer.key 4096
openssl rsa -in sa-signer.key -pubout -out sa-signer.pub
```

Create a JWKS file from the public key:

```bash
# Extract modulus and compute key ID
HEX_MODULUS=$(openssl rsa -in sa-signer.key -pubout -outform DER 2>/dev/null | \
  openssl rsa -pubin -inform DER -text -noout 2>/dev/null | \
  grep -A 100 "^Modulus:" | grep -v "^Modulus:" | grep -v "^Exponent:" | \
  tr -d ' \n:' | sed 's/^00//')
MODULUS=$(printf '%b' "$(echo "$HEX_MODULUS" | sed 's/../\\x&/g')" | base64 -w0 | tr '+/' '-_' | tr -d '=')
KID=$(openssl rsa -in sa-signer.key -pubout -outform DER 2>/dev/null | \
  openssl dgst -sha256 -binary | base64 -w0 | tr '+/' '-_' | tr -d '=')

cat > jwks.json << EOF
{
  "keys": [
    {
      "kty": "RSA",
      "alg": "RS256",
      "use": "sig",
      "kid": "${KID}",
      "n": "${MODULUS}",
      "e": "AQAB"
    }
  ]
}
EOF
```

## Create IAM Resources

The `hypershift create iam gcp` command creates WIF resources in the hosted cluster project:

- **Workload Identity Pool** — Container for workload identity providers
- **OIDC Provider** — Links the hosted cluster's Kubernetes OIDC issuer to GCP IAM
- **Service Accounts** — GCP service accounts for hosted cluster components:
  - `controlplane` — Control Plane Operator (DNS admin, network admin)
  - `nodepool` — CAPG controller (compute instance admin, network admin)
  - `cloud-controller` — Cloud Controller Manager (load balancer admin, security admin, compute viewer)
  - `storage` — GCP PD CSI Driver (storage admin, instance admin)
  - `image-registry` — Image Registry Operator (storage admin)
  - `cloud-network` — Cloud Network Config Controller (instance admin, network user)

```bash
hypershift create iam gcp \
  --infra-id=<infra-id> \
  --project-id=<hosted-cluster-project-id> \
  --oidc-jwks-file=jwks.json
```

!!! warning "Reserved prefix"

    The `--infra-id` value must not start with `gcp-` — GCP reserves this prefix for Workload Identity Pool IDs.

### Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--infra-id` | Yes | Infrastructure ID (must match the value used for `create infra gcp`) |
| `--project-id` | Yes | GCP project ID where WIF resources will be created |
| `--oidc-jwks-file` | Yes | Path to JWKS JSON file containing the OIDC provider's public key |
| `--oidc-issuer-url` | No | Custom OIDC issuer URL (defaults to `https://hypershift-<infra-id>-oidc`) |
| `--output-file` | No | Path to save output JSON with WIF configuration |

### Example

```bash
hypershift create iam gcp \
  --infra-id=my-cluster \
  --project-id=my-hc-project \
  --oidc-jwks-file=jwks.json \
  > iam-output.json
```

### Output

The command outputs JSON with the WIF configuration:

```json
{
  "projectId": "my-hc-project",
  "projectNumber": "123456789",
  "infraId": "my-cluster",
  "workloadIdentityPool": {
    "poolId": "my-cluster-wi-pool",
    "providerId": "my-cluster-k8s-provider"
  },
  "serviceAccounts": {
    "ctrlplane-op": "my-cluster-ctrlplane-op@my-hc-project.iam.gserviceaccount.com",
    "nodepool-mgmt": "my-cluster-nodepool-mgmt@my-hc-project.iam.gserviceaccount.com",
    "cloud-controller": "my-cluster-cloud-controller@my-hc-project.iam.gserviceaccount.com",
    "gcp-pd-csi": "my-cluster-gcp-pd-csi@my-hc-project.iam.gserviceaccount.com",
    "image-registry": "my-cluster-image-registry@my-hc-project.iam.gserviceaccount.com",
    "cloud-network": "my-cluster-cloud-network@my-hc-project.iam.gserviceaccount.com"
  }
}
```

Save this output — you will need the project number, pool/provider IDs, and service account emails when creating the hosted cluster.

## Destroy IAM Resources

To clean up WIF resources:

```bash
hypershift destroy iam gcp \
  --infra-id=<infra-id> \
  --project-id=<hosted-cluster-project-id>
```

## Next Steps

- [Create a GCP Hosted Cluster](create-gcp-hosted-cluster.md) — Deploy your hosted cluster using the infrastructure and IAM resources
