# HyperShift Operator Installation

This document describes different installation flags or methods for HyperShift Operator (HO).

## Limiting the CAPI CRDs installed
The HO uses the Cluster API (CAPI) to manage the nodes in the NodePool. By default, the HO installation will install all 
CAPI related CRDs. If you want to limit the CRDs installed, you can set the `--limit-crd-install` flag to a 
comma-separated list of CRDs to install. The valid values for this flag are: AWS, Azure, IBMCloud, KubeVirt, Agent, 
OpenStack.

For example, to only install the AWS and Azure related CAPI CRDs, you would use 
the following flag in your HO install command:

```bash
--limit-crd-install=AWS,Azure
```

!!! important

    Limiting the CAPI CRDs installed means the HO will only be able to manage HostedClusters of the same platform.
    For example, in the above example, if you limit the CRDs to AWS and Azure, the HO will only be able to manage 
    AWS and Azure HostedClusters.

## AWS operator IAM roles

When installing the HyperShift operator on AWS, the operator and its external-dns
component need AWS credentials. Instead of providing long-lived credential files,
you can create least-privilege IAM roles and pass their ARNs to the install
command. The `hypershift create operator-roles aws` command automates role
creation, and the `hypershift install` command accepts role ARN flags as
alternatives to credential file flags.

### Creating operator roles

The `hypershift create operator-roles aws` command creates three IAM roles:

| Role | Purpose |
|------|---------|
| `<prefix>-operator-ec2` | EC2 VPC endpoint service management, ELB discovery, and instance type queries |
| `<prefix>-operator-oidc-s3` | Upload and delete OIDC discovery documents in the S3 bucket |
| `<prefix>-external-dns` | Route53 DNS record management for external-dns |

The command supports two trust modes, depending on whether the management
cluster has an OIDC provider registered in AWS IAM:

- **OIDC web identity** (default): The roles trust the management cluster's OIDC
  provider and are scoped to specific service accounts. If `--oidc-issuer-url` is
  not specified, the command auto-discovers it from the management cluster's
  `Authentication` CR.
- **Instance role assumption**: For clusters without an OIDC provider, use
  `--instance-role-arn` to trust an existing instance role via `sts:AssumeRole`.

#### Usage

```bash
hypershift create operator-roles aws \
  --oidc-storage-provider-s3-bucket-name <bucket-name> \
  [--region <aws-region>] \
  [--name-prefix <prefix>] \
  [--oidc-issuer-url <url> | --instance-role-arn <arn>] \
  [--route53-hosted-zone-id <zone-id>] \
  [--operator-namespace <namespace>] \
  [--output-file <path>] \
  [--additional-tags key=value,...]
```

#### Flags

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--oidc-storage-provider-s3-bucket-name` | Yes | | S3 bucket name for OIDC documents (scopes the S3 policy) |
| `--region` | No | `AWS_REGION`, `AWS_DEFAULT_REGION`, or `~/.aws/config` | AWS region |
| `--name-prefix` | No | `hypershift` | Prefix for IAM role names |
| `--oidc-issuer-url` | No | Auto-discovered from cluster | Management cluster OIDC issuer URL for web identity trust. Mutually exclusive with `--instance-role-arn` |
| `--instance-role-arn` | No | | ARN of an instance role to trust via `sts:AssumeRole`. Mutually exclusive with `--oidc-issuer-url` |
| `--route53-hosted-zone-id` | No | All zones | Route53 hosted zone ID to scope the external-dns policy |
| `--operator-namespace` | No | `hypershift` | Namespace where the HyperShift operator is installed |
| `--output-file` | No | stdout | Path to write JSON output |
| `--additional-tags` | No | | Additional tags to set on IAM resources (`key=value`) |

#### Output

The command outputs a JSON object with the ARNs of the created roles:

```json
{
  "operatorEC2RoleARN": "arn:aws:iam::123456789012:role/hypershift-operator-ec2",
  "operatorOIDCS3RoleARN": "arn:aws:iam::123456789012:role/hypershift-operator-oidc-s3",
  "externalDNSRoleARN": "arn:aws:iam::123456789012:role/hypershift-external-dns"
}
```

#### Example: OIDC web identity (auto-discovered)

```bash
hypershift create operator-roles aws \
  --oidc-storage-provider-s3-bucket-name my-oidc-bucket \
  --route53-hosted-zone-id Z0123456789ABCDEF \
  --output-file operator-roles.json
```

#### Example: Instance role trust

```bash
hypershift create operator-roles aws \
  --oidc-storage-provider-s3-bucket-name my-oidc-bucket \
  --instance-role-arn arn:aws:iam::123456789012:role/my-instance-role \
  --output-file operator-roles.json
```

### Installing with role ARNs

The `hypershift install` command accepts IAM role ARNs as alternatives to
credential file flags. When a role ARN is provided, the installer generates
a credential file automatically based on `--aws-role-credential-source`.

#### Role ARN install flags

| Flag | Replaces | Description |
|------|----------|-------------|
| `--aws-private-role-arn` | `--aws-private-creds` | IAM role ARN for EC2/ELBv2 credentials |
| `--oidc-storage-provider-s3-role-arn` | `--oidc-storage-provider-s3-credentials` | IAM role ARN for OIDC S3 access |
| `--external-dns-role-arn` | `--external-dns-credentials` | IAM role ARN for external-dns Route53 access |
| `--aws-role-credential-source` | | Credential source: `web-identity` (default) or `ec2-instance-metadata` |
| `--aws-operator-roles-file` | | Path to JSON output from `create operator-roles aws` (sets all three role ARN flags at once) |

!!! note

    Each role ARN flag is mutually exclusive with its corresponding credential
    file flag. You cannot combine `--aws-private-role-arn` with
    `--aws-private-creds`, for example.

#### Credential source modes

The `--aws-role-credential-source` flag controls how the operator assumes the
IAM roles:

- **`web-identity`** (default): Uses a projected service account token mounted
  at `/var/run/secrets/openshift/serviceaccount/token`. Requires the management
  cluster's OIDC provider to be registered in AWS IAM.
- **`ec2-instance-metadata`**: Uses the EC2 instance metadata service to assume
  the role. Suitable for clusters running on EC2 instances that have an instance
  profile attached.

#### Example: Install using the roles file

The simplest workflow combines `create operator-roles aws` with `install`:

```bash
# Step 1: Create the IAM roles
hypershift create operator-roles aws \
  --oidc-storage-provider-s3-bucket-name my-oidc-bucket \
  --output-file operator-roles.json

# Step 2: Install using the roles file
hypershift install \
  --oidc-storage-provider-s3-bucket-name my-oidc-bucket \
  --oidc-storage-provider-s3-region us-east-1 \
  --private-platform AWS \
  --aws-private-region us-east-1 \
  --external-dns-provider aws \
  --external-dns-domain-filter example.com \
  --aws-operator-roles-file operator-roles.json
```
