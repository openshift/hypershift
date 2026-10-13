---
name: Install HO AWS
description: "Install HyperShift Operator with private AWS and external-dns settings."
---

# Install HyperShift Operator (HO)

Use this skill to install HyperShift Operator with a custom image, external-dns, and private AWS settings.

## When to Use This Skill

Use when:
- You need to install HO with a custom image
- You want external-dns configured for AWS
- You are using private AWS settings for the management cluster
- Changes to the CRDs generated APIs don't need an image rebuild, just a make api && make build

## Prerequisites

Source the environment file before using this skill:

```bash
source dev/claude-env.sh
```

## Environment Configuration

Environment variables from `dev/claude-env.sh`:

| Variable | Description |
|----------|-------------|
| `HO_IMAGE_REPO` | Container registry for HO images |
| `AWS_CREDENTIALS` | Path to AWS credentials file |
| `EXTERNAL_DNS_DOMAIN` | Domain filter for external-dns |
| `OIDC_BUCKET` | S3 bucket for OIDC |
| `AWS_REGION` | AWS region |
| `MGMT_KUBECONFIG` | Path to management cluster kubeconfig |

## Parameters

- `HO_IMAGE` should point to the image you want to install, for example `$HO_IMAGE_REPO:autonode`.

## Command

Run make build first if needed

```bash
KUBECONFIG=$MGMT_KUBECONFIG \
./bin/hypershift install \
  --hypershift-image $HO_IMAGE_REPO:YOUR_TAG \
  --external-dns-provider=aws \
  --external-dns-credentials $AWS_CREDENTIALS \
  --external-dns-domain-filter=$EXTERNAL_DNS_DOMAIN \
  --oidc-storage-provider-s3-bucket-name $OIDC_BUCKET \
  --oidc-storage-provider-s3-credentials $AWS_CREDENTIALS \
  --oidc-storage-provider-s3-region $AWS_REGION \
  --private-platform=AWS \
  --aws-private-creds $AWS_CREDENTIALS \
  --enable-conversion-webhook=false \
  --aws-private-region=$AWS_REGION
```

## Notes

- Build the CLI first: `make hypershift` (this produces `./bin/hypershift`).
- Ensure the AWS credentials file exists and is readable.
- The `MGMT_KUBECONFIG` must point to your management cluster.
