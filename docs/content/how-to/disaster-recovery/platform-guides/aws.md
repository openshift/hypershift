---
title: AWS Platform Guide
---

# AWS Disaster Recovery Guide

This page documents AWS-specific configuration, caveats, and post-restore procedures for HostedCluster disaster recovery.

For the general backup and restore procedures, see:

- [Same-cluster Restore](../scenarios/same-cluster-restore.md)
- [Cross-cluster Migration](../scenarios/cross-cluster-migration.md)

## AWS-Specific Prerequisites

In addition to the [general prerequisites](../prerequisites.md):

- IAM roles and policies for S3 backup storage must be configured. Follow the [official OADP AWS documentation](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/installing/installing-oadp-aws.html#migration-configuring-aws-s3_installing-oadp-aws).
- If using ExternalDNS, ensure the ExternalDNS Operator is deployed with the correct domain and AWS credentials.

## Endpoint Access Considerations

The DR procedure varies depending on the HostedCluster's `endpointAccess` configuration:

| Endpoint Access | ExternalDNS Required | Route Cleanup Required | DNS Update Method |
| ---------------- | --------------------- | ---------------------- | ------------------- |
| **Public** | Yes | Yes (before cross-cluster migration) | ExternalDNS auto-updates |
| **PublicAndPrivate** | Yes | Yes (before cross-cluster migration) | ExternalDNS auto-updates |
| **Private** | No | No | Manual PrivateLink DNS update |

### HyperShift Operator Deployment Arguments

Depending on endpoint access, the HyperShift Operator must be deployed with specific arguments:

=== "**Public / PublicAndPrivate**"

    ```bash
    hypershift install \
      --external-dns-provider=aws \
      --external-dns-credentials=<AWS_CREDENTIALS_PATH> \
      --external-dns-domain-filter=<EXTERNAL_DNS_DOMAIN>
    ```

=== "**Private**"

    ```bash
    hypershift install \
      --private-platform aws \
      --aws-private-creds <AWS_CREDENTIALS_PATH> \
      --aws-private-region <AWS_REGION>
    ```

### Service Publishing Strategy for AWS

For AWS self-managed platforms, the APIServer can use either a **LoadBalancer** or a **Route** publishing strategy:

```yaml
# Option 1: LoadBalancer (default)
spec:
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
      loadBalancer:
        hostname: api.example.com

# Option 2: Route (AWS self-managed only)
spec:
  platform:
    aws:
      endpointAccess: Public
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: Route
      route:
        hostname: api.example.com
```

## OADP DPA Configuration for AWS

```yaml
apiVersion: oadp.openshift.io/v1alpha1
kind: DataProtectionApplication
metadata:
  name: dpa-instance
  namespace: openshift-adp
spec:
  backupLocations:
    - name: default
      velero:
        provider: aws
        default: true
        objectStorage:
          bucket: <bucket_name>
          prefix: <prefix>
        config:
          region: us-east-1
          profile: "backupStorage"
        credential:
          key: cloud
          name: cloud-credentials
  configuration:
    nodeAgent:
      enable: true
      uploaderType: kopia
    velero:
      defaultPlugins:
        - openshift
        - aws
        - csi
        - hypershift
      resourceTimeout: 2h
```

## AWS-Specific Backup Resources

When creating a Velero Backup for an AWS HostedCluster, ensure the following AWS CAPI resources are included in `includedResources`:

```yaml
- awscluster
- awsmachinetemplate
- awsmachine
```

See the [OADP method reference](../methods/oadp.md) for the complete backup manifest.

## Fixing OIDC After Restore

After restoring a HostedCluster via OADP on AWS, the IAM OIDC identity provider and its S3 discovery documents may be missing or inconsistent. This causes the control-plane-operator to fail with `WebIdentityErr` and prevents the default security group from being reconciled, leaving NodePool nodes in a not-ready state.

### Symptoms

- `control-plane-operator` logs show `WebIdentityErr` errors.
- NodePool nodes stay in `NotReady` state.
- The default security group is not being reconciled.

### Fix

Run the `hypershift fix dr-oidc-iam` command:

```bash
# Auto-detect configuration from the HostedCluster
hypershift fix dr-oidc-iam \
  --hc-name <cluster-name> \
  --hc-namespace <namespace> \
  --aws-creds ~/.aws/credentials
```

Available options:

| Flag | Description |
| ------ | ------------- |
| `--dry-run` | Preview changes without applying them |
| `--force-recreate` | Force complete regeneration of OIDC documents and provider |
| `--restart-delay` | Adjust the delay before rolling restart (default: 5m) |

### What the Command Does

1. Checks if OIDC discovery documents exist in S3.
2. Checks if the IAM OIDC identity provider exists.
3. Ensures the S3 bucket is properly configured with public read access.
4. Retrieves the existing service account signing public key from the `sa-signing-key` secret.
5. Generates and uploads OIDC discovery and JWKS documents using the existing key.
6. Creates or recreates the IAM OIDC identity provider.
7. Verifies the configuration and schedules a rolling restart of the HostedCluster.

## Cross-Cluster Migration: Route Cleanup

When performing cross-cluster migration for `Public` or `PublicAndPrivate` clusters, you must clean up the control plane Routes **before** teardown so that the ExternalDNS Operator removes the Route53 entries:

```bash
oc delete routes -n <HC_NAMESPACE>-<HC_NAME> --all
```

Wait for the DNS records to be cleaned up:

```bash
# Monitor Route53 record count
watch "aws route53 list-resource-record-sets --hosted-zone-id <ZONE_ID> \
  --max-items 10000 --output json | grep -c <EXTERNAL_DNS_DOMAIN>"
```

The count should drop to the baseline (typically 2 SOA/NS records) before proceeding.

## Node Readoption

Node readoption is **not supported** on AWS. Worker nodes will be reprovisioned during restore.

## Migration Helper Script

A migration helper script is maintained at:
[https://github.com/openshift/hypershift/blob/main/contrib/migration/migrate-hcp.sh](https://github.com/openshift/hypershift/blob/main/contrib/migration/migrate-hcp.sh)
