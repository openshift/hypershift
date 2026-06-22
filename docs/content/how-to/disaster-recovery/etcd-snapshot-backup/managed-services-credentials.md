---
title: Managed Services Credentials
---

# Managed Services Credential Configuration

!!! warning "Tech Preview"

    This feature requires the `HCPEtcdBackup` feature gate enabled in the HyperShift Operator.

The HCPEtcdBackup controller automatically detects the authentication mode from the credential Secret referenced in the backup specification. This page describes how to configure credentials for managed service platforms (ROSA HCP and ARO HCP) that use short-lived, federated credentials instead of long-lived static keys.

## OADP Plugin Secret Flow

The credential Secret originates in the OADP namespace (typically `openshift-adp`) and is always named `cloud-credentials`. It always uses the `cloud` data key. When the OADP HyperShift plugin triggers an etcd snapshot backup, it copies this Secret to the HyperShift Operator namespace, remapping the key and preserving the original:

| | Namespace | Secret Name | Data Keys |
|---|-----------|-------------|-----------|
| **Source** | `<OADP_NAMESPACE/VELERO_NAMESPACE>` | `cloud-credentials` | `cloud` |
| **Destination** | `hypershift` (HO namespace) | `cloud-credentials` | `credentials` (remapped from `cloud`) + `cloud` (preserved) |

The destination Secret contains both keys with the same content. The controller's credential auto-detection logic reads from the destination copy:

- For **S3** storage: reads the `credentials` key (remapped from `cloud`)
- For **Azure Blob** storage: checks the `cloud` key first (preserved original), then falls back to `credentials`

No manual Secret creation or copying is required — the OADP plugin handles this automatically for both ROSA HCP and ARO HCP.

## Credential Auto-Detection

The controller inspects the content of the credential Secret to determine the authentication mode. No explicit configuration flag is required — the Secret format itself drives the behavior.

### AWS Credential Modes

| Mode | Detection | PodSpec Behavior |
|------|-----------|------------------|
| **Static** | `credentials` key contains an AWS credentials file (`[default]` profile) | Mounts credentials file at `/etc/etcd-backup-creds/credentials`, passes `--credentials-file` |
| **STS/IRSA** | `credentials` key contains a bare IAM role ARN (`arn:aws:iam::...`) | Projected SA token volume (`sts.amazonaws.com` audience), `AWS_ROLE_ARN` and `AWS_WEB_IDENTITY_TOKEN_FILE` env vars, no credentials file |

### Azure Credential Modes

| Mode | Detection | PodSpec Behavior |
|------|-----------|------------------|
| **Workload Identity** | Secret has a `cloud` key containing a non-empty `AZURE_CLIENT_ID=` value | Pod label `azure.workload.identity/use=true`, SA annotated with `azure.workload.identity/client-id`, no credentials file, no `--azure-auth-type` flag |
| **Client Secret** | `credentials` key contains JSON with a non-empty `clientSecret` field | Mounts credentials file, passes `--credentials-file` and `--azure-auth-type client-secret` |
| **Managed Identity** | `credentials` key contains JSON without a `clientSecret` field | Mounts credentials file, passes `--credentials-file` and `--azure-auth-type managed-identity` |

## ROSA HCP (AWS STS/IRSA)

ROSA HCP clusters use IAM Roles for Service Accounts (IRSA) via AWS Security Token Service (STS). The backup Job Pod authenticates to S3 using a projected ServiceAccount token exchanged for temporary AWS credentials.

### Prerequisites

1. An S3 bucket for storing etcd snapshots
2. An IAM role with a trust policy allowing the OIDC provider associated with your management cluster
3. The IAM role must have permissions to write objects to the target S3 bucket

#### IAM Role Trust Policy

The trust policy must allow the `etcd-backup-job` ServiceAccount in the HyperShift Operator namespace to assume the role:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::<ACCOUNT_ID>:oidc-provider/<OIDC_PROVIDER>"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "<OIDC_PROVIDER>:sub": "system:serviceaccount:<HO_NAMESPACE>:etcd-backup-job"
        }
      }
    }
  ]
}
```

Replace:

- `<ACCOUNT_ID>`: Your AWS account ID
- `<OIDC_PROVIDER>`: The OIDC provider URL for your management cluster (without `https://`)
- `<HO_NAMESPACE>`: The namespace where the HyperShift Operator runs (typically `hypershift`)

#### IAM Role Permissions

The role needs S3 write access:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:PutObject",
        "s3:AbortMultipartUpload"
      ],
      "Resource": [
        "arn:aws:s3:::<BUCKET_NAME>",
        "arn:aws:s3:::<BUCKET_NAME>/*"
      ]
    }
  ]
}
```

!!! note

    The `s3:PutObject` permission authorizes all multipart upload operations (`CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`) used by the AWS SDK v2 Transfer Manager, which automatically splits files larger than 5 MB into multipart uploads. Etcd snapshots typically range from 30-100 MB, so multipart upload is used in practice. `s3:AbortMultipartUpload` is included to clean up incomplete uploads on failure.

### Credential Secret Format

The credential Secret in the OADP namespace uses the `cloud` key with the IAM role ARN:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cloud-credentials
  namespace: <OADP_NAMESPACE/VELERO_NAMESPACE>
type: Opaque
stringData:
  cloud: "arn:aws:iam::<ACCOUNT_ID>:role/<ROLE_NAME>"
```

The OADP plugin copies this Secret to the HyperShift Operator namespace, remapping `cloud` → `credentials` and preserving the original `cloud` key (see [OADP Plugin Secret Flow](#oadp-plugin-secret-flow)). The controller reads the `credentials` key in the destination copy and detects STS mode when the value starts with `arn:`.

!!! note

    The `cloud` value must be a bare ARN string, not an AWS credentials file.

### HCPEtcdBackup CR

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HCPEtcdBackup
metadata:
  name: my-backup
  namespace: <HCP_NAMESPACE>
spec:
  storage:
    storageType: S3
    s3:
      bucket: <BUCKET_NAME>
      region: <REGION>
      keyPrefix: etcd-backups
      credentials:
        name: cloud-credentials
```

### What Happens at Runtime

When the controller detects STS mode:

1. The ServiceAccount `etcd-backup-job` is created in the HO namespace (no special annotations needed for AWS IRSA — the projected token handles authentication)
2. The Job Pod gets a projected volume with a ServiceAccount token using audience `sts.amazonaws.com` and 1-hour expiration
3. The upload container receives environment variables `AWS_ROLE_ARN` and `AWS_WEB_IDENTITY_TOKEN_FILE`
4. No credentials file is mounted — the AWS SDK uses the projected token to assume the role via STS

## ARO HCP (Azure Workload Identity)

ARO HCP clusters use Azure AD Workload Identity for pod-level authentication. The backup Job Pod authenticates to Azure Blob Storage using a federated token projected by the Azure Workload Identity webhook.

### Prerequisites

1. An Azure Storage Account with a blob container for storing etcd snapshots
2. A User-Assigned Managed Identity with a federated credential configured for the backup Job's ServiceAccount
3. The managed identity must have `Storage Blob Data Contributor` role on the storage account

#### Federated Credential

Create a federated credential on the managed identity that trusts the `etcd-backup-job` ServiceAccount:

```bash
az identity federated-credential create \
  --name etcd-backup-fedcred \
  --identity-name <MANAGED_IDENTITY_NAME> \
  --resource-group <RESOURCE_GROUP> \
  --issuer <OIDC_ISSUER_URL> \
  --subject "system:serviceaccount:<HO_NAMESPACE>:etcd-backup-job" \
  --audiences "api://AzureADTokenExchange"
```

Replace:

- `<MANAGED_IDENTITY_NAME>`: Name of the User-Assigned Managed Identity
- `<RESOURCE_GROUP>`: Resource group containing the managed identity
- `<OIDC_ISSUER_URL>`: The OIDC issuer URL of your management cluster
- `<HO_NAMESPACE>`: The namespace where the HyperShift Operator runs (typically `hypershift`)

!!! important

    The subject must match exactly: `system:serviceaccount:<HO_NAMESPACE>:etcd-backup-job`. The Job runs in the HO namespace, not the HCP namespace.

#### Storage Account Role Assignment

Assign the `Storage Blob Data Contributor` role to the managed identity on the storage account:

```bash
az role assignment create \
  --assignee-object-id $(az identity show -n <MANAGED_IDENTITY_NAME> -g <RESOURCE_GROUP> --query principalId -o tsv) \
  --role "Storage Blob Data Contributor" \
  --scope /subscriptions/<SUBSCRIPTION_ID>/resourceGroups/<RESOURCE_GROUP>/providers/Microsoft.Storage/storageAccounts/<STORAGE_ACCOUNT>
```

### Credential Secret Format

The credential Secret in the OADP namespace uses the `cloud` key with Azure identity configuration in key-value format:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cloud-credentials
  namespace: <OADP_NAMESPACE/VELERO_NAMESPACE>
type: Opaque
stringData:
  cloud: |
    AZURE_SUBSCRIPTION_ID=<SUBSCRIPTION_ID>
    AZURE_TENANT_ID=<TENANT_ID>
    AZURE_CLIENT_ID=<MANAGED_IDENTITY_CLIENT_ID>
    AZURE_RESOURCE_GROUP=<RESOURCE_GROUP>
    AZURE_CLOUD_NAME=AzurePublicCloud
```

The OADP plugin copies this Secret to the HyperShift Operator namespace, remapping `cloud` → `credentials` and preserving the original `cloud` key (see [OADP Plugin Secret Flow](#oadp-plugin-secret-flow)). The controller reads the destination copy and detects Workload Identity mode when it finds the `cloud` key. It extracts the `AZURE_CLIENT_ID` value to annotate the ServiceAccount.

!!! note

    Fields like `AZURE_FEDERATED_TOKEN_FILE` and `AZURE_AUTHORITY_HOST` are not needed in the Secret — they are injected at runtime by the Azure Workload Identity webhook into the Pod environment.

### HCPEtcdBackup CR

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HCPEtcdBackup
metadata:
  name: my-backup
  namespace: <HCP_NAMESPACE>
spec:
  storage:
    storageType: AzureBlob
    azureBlob:
      container: <CONTAINER_NAME>
      storageAccount: <STORAGE_ACCOUNT>
      keyPrefix: etcd-backups
      credentials:
        name: cloud-credentials
```

### What Happens at Runtime

When the controller detects Azure Workload Identity mode:

1. The ServiceAccount `etcd-backup-job` is created in the HO namespace with annotation `azure.workload.identity/client-id: <CLIENT_ID>`
2. The Job Pod template gets the label `azure.workload.identity/use: "true"`
3. The Azure Workload Identity webhook mutates the Pod to inject:
    - A projected volume with a federated token
    - Environment variables `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_FEDERATED_TOKEN_FILE`, `AZURE_AUTHORITY_HOST`
4. No credentials file is mounted and no `--azure-auth-type` flag is passed — the Azure SDK uses the injected federated token

## OADP Plugin Integration

The OADP HyperShift plugin handles credential propagation automatically for both ROSA HCP and ARO HCP. No manual Secret creation or copying is required. The plugin copies the `cloud-credentials` Secret from the OADP namespace to the HyperShift Operator namespace, performing key remapping as described in the [OADP Plugin Secret Flow](#oadp-plugin-secret-flow) section above. The controller then auto-detects the credential mode from the destination Secret content.

## Troubleshooting

### AWS STS: Access Denied

```text
An error occurred (AccessDenied) when calling the <S3_OPERATION> operation
```

- Verify the IAM role trust policy allows the correct OIDC provider and ServiceAccount subject
- Confirm the IAM role has all required S3 permissions on the target bucket (`s3:PutObject`, `s3:AbortMultipartUpload`) — see [IAM Role Permissions](#iam-role-permissions)
- Check that the OIDC provider URL matches the management cluster's issuer

### Azure WI: No Matching Federated Identity Record

```text
AADSTS700213: No matching federated identity record found for presented assertion subject
```

- Verify the federated credential subject matches exactly: `system:serviceaccount:<HO_NAMESPACE>:etcd-backup-job`
- The Job runs in the HO namespace (e.g., `hypershift`), not the HCP namespace
- Confirm the OIDC issuer URL matches the management cluster's issuer
- Check the audience is set to `api://AzureADTokenExchange`

### Secret Key Mismatch

If the controller falls through to the wrong credential mode, check the destination Secret (in the HO namespace) has the expected keys after plugin copying:

- AWS: must have a `credentials` key (remapped from `cloud` by the plugin)
- Azure WI: must have a `cloud` key (preserved by the plugin; the presence of this key triggers WI mode). `AZURE_CLIENT_ID=` should be included in the value for SA annotation
- Azure Client Secret: must have a `credentials` key with JSON containing `clientSecret`

### Job Pods Not Starting

Check the ServiceAccount and its annotations:

```bash
kubectl get sa etcd-backup-job -n <HO_NAMESPACE> -o yaml
```

- For Azure WI: the SA must have annotation `azure.workload.identity/client-id`
- For AWS STS: verify the projected volume appears in the Pod spec

### Verifying PodSpec

Inspect the generated Job to confirm the correct credential mode was applied:

```bash
kubectl get job -n <HO_NAMESPACE> -l app=etcd-backup -o yaml
```

- **AWS STS**: Look for `AWS_ROLE_ARN` env var and `aws-iam-token` projected volume
- **Azure WI**: Look for pod label `azure.workload.identity/use: "true"` and no `--credentials-file` in container args
- **Static/Client Secret**: Look for `backup-credentials` volume and `--credentials-file` in container args
