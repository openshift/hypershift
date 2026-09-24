---
title: StorageClass KMS Encryption
---

# Encrypting the Default StorageClass with a KMS Key

## Background

By default, EBS volumes provisioned by a hosted cluster's default StorageClass use
AWS-managed encryption. You can instead encrypt them with a customer-managed AWS KMS
key by setting
[`initialKMSKeyARN`](../../reference/api.md#hypershift.openshift.io/v1beta1.AWSCSIDriverConfig)
(or `--initial-storage-volumes-kms-key`) at cluster creation. The key is written to the
hosted cluster's `ClusterCSIDriver`, which the cluster-storage-operator uses to
configure the default `gp3-csi` StorageClass.

You choose this key when you first create the cluster, and it stays fixed for the
life of the cluster. You cannot change or remove it later by editing the
HostedCluster. If you need to switch to a different key after the cluster is
running, update the `ClusterCSIDriver` resource inside the hosted cluster directly.

## IAM permissions

The IAM role set in `spec.platform.aws.rolesRef.storageARN` (used by the AWS EBS CSI
driver) needs the following permissions on the KMS key set in
[`initialKMSKeyARN`](../../reference/api.md#hypershift.openshift.io/v1beta1.AWSCSIDriverConfig)
(or via `--initial-storage-volumes-kms-key`):

- `kms:Decrypt`
- `kms:GenerateDataKeyWithoutPlaintext`
- `kms:CreateGrant`

When `hypershift create cluster aws` creates the IAM roles, the storage role already
receives these permissions and no extra action is needed. With
`--use-rosa-managed-policies`, the storage role uses the AWS managed policy
`ROSAAmazonEBSCSIDriverOperatorPolicy`, which only grants these permissions on keys
tagged `red-hat=true`, so tag the key accordingly. If you bring your own IAM
roles, make sure the storage role's policy grants these actions on the key, for
example:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "kms:Decrypt",
        "kms:GenerateDataKeyWithoutPlaintext",
        "kms:CreateGrant"
      ],
      "Resource": "arn:aws:kms:us-east-1:123456789012:key/<key-id>"
    }
  ]
}
```

If the key is disabled or deleted, its key policy does not allow the storage role, or
these permissions are missing, PVC provisioning fails at the CSI driver level, with the
AWS error surfaced in the PVC events. This is the same behavior as standalone OpenShift.

## Creating the cluster

Pass the KMS key ARN (or alias ARN) via `--initial-storage-volumes-kms-key`:

```shell
hypershift create cluster aws \
  --name my-cluster \
  --initial-storage-volumes-kms-key arn:aws:kms:us-east-1:123456789012:key/<key-id> \
  ... # other required flags
```

The ARN must match the format
`arn:<partition>:kms:<region>:<account-id>:(key|alias)/<id>`, use one of the `aws`,
`aws-cn`, `aws-us-gov`, or `aws-iso*` partitions, and point to a key in the same
region as the cluster (`--region`).

## Verifying encryption

After the cluster is up and a node has joined, provision a PVC using the default
StorageClass and confirm the backing EBS volume is encrypted with the configured key:

```shell
# In the hosted cluster:
oc get storageclass gp3-csi -o jsonpath='{.parameters}'
# Expect: {"encrypted":"true","kmsKeyId":"arn:aws:kms:...","type":"gp3"}

# After binding a PVC, find the EBS volume and check encryption:
aws ec2 describe-volumes --volume-ids <vol-id> \
  --query 'Volumes[0].{Encrypted:Encrypted,KmsKeyId:KmsKeyId}'
```
