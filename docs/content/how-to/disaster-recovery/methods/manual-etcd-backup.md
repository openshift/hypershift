---
title: Manual Etcd Backup and Restore
---

# Manual Etcd Backup and Restore

This is a fully manual process for snapshotting and restoring the etcd state of a HostedCluster. It requires API downtime and direct interaction with etcd pods.

!!! note

    For automated backup and restore, use the [OADP method](oadp.md) or [Etcd Snapshot Backup (Tech Preview)](etcd-snapshot/index.md) instead. This manual procedure is provided as a fallback for environments where OADP is not available.

## Creating the Etcd Snapshot

### Step 1: Pause the HostedCluster

[Pause reconciliation](../../pause-reconciliation.md) of the HostedCluster:

```bash
oc patch -n clusters hostedclusters/${CLUSTER_NAME} \
  -p '{"spec":{"pausedUntil":"'${PAUSED_UNTIL}'"}}' --type=merge
```

### Step 2: Stop Etcd Writers

Scale down all deployments that write to etcd:

```bash
oc scale deployment -n ${HOSTED_CLUSTER_NAMESPACE} --replicas=0 \
  kube-apiserver openshift-apiserver openshift-oauth-apiserver
```

### Step 3: Take the Snapshot

Perform an etcd snapshot via `exec` in any etcd container:

```bash
oc exec -it etcd-0 -n ${HOSTED_CLUSTER_NAMESPACE} -- env ETCDCTL_API=3 \
  /usr/bin/etcdctl \
  --cacert /etc/etcd/tls/etcd-ca/ca.crt \
  --cert /etc/etcd/tls/client/etcd-client.crt \
  --key /etc/etcd/tls/client/etcd-client.key \
  --endpoints=localhost:2379 \
  snapshot save /var/lib/data/snapshot.db
```

Verify the snapshot:

```bash
oc exec -it etcd-0 -n ${HOSTED_CLUSTER_NAMESPACE} -- env ETCDCTL_API=3 \
  /usr/bin/etcdctl -w table snapshot status /var/lib/data/snapshot.db
```

!!! warning "Etcd CA Certificate Location"

    The CA certificate location changed in OCP 4.12:

    - **OCP 4.12+**: `/etc/etcd/tls/etcd-ca/ca.crt`
    - **OCP 4.11 and earlier**: `/etc/etcd/tls/client/etcd-client-ca.crt`

    Check your version with: `oc version -o json | jq -e .openshiftVersion`

### Step 4: Upload to Object Storage

Upload the snapshot to S3 (or another object store):

```bash
BUCKET_NAME=somebucket
FILEPATH="/${BUCKET_NAME}/${CLUSTER_NAME}-snapshot.db"
CONTENT_TYPE="application/x-compressed-tar"
DATE_VALUE=$(date -R)
SIGNATURE_STRING="PUT\n\n${CONTENT_TYPE}\n${DATE_VALUE}\n${FILEPATH}"
ACCESS_KEY=accesskey
SECRET_KEY=secret
SIGNATURE_HASH=$(echo -en ${SIGNATURE_STRING} | openssl sha1 -hmac ${SECRET_KEY} -binary | base64)

oc exec -it etcd-0 -n ${HOSTED_CLUSTER_NAMESPACE} -- curl -X PUT -T "/var/lib/data/snapshot.db" \
  -H "Host: ${BUCKET_NAME}.s3.amazonaws.com" \
  -H "Date: ${DATE_VALUE}" \
  -H "Content-Type: ${CONTENT_TYPE}" \
  -H "Authorization: AWS ${ACCESS_KEY}:${SIGNATURE_HASH}" \
  https://${BUCKET_NAME}.s3.amazonaws.com/${CLUSTER_NAME}-snapshot.db
```

### Step 5: Save the Encryption Key

If you plan to restore the snapshot later, you **must** save the etcd encryption key:

```bash
# Find the encryption secret name
oc get hostedcluster $CLUSTER_NAME -o=jsonpath='{.spec.secretEncryption.aescbc}'

# Save the key
oc get secret ${CLUSTER_NAME}-etcd-encryption-key -o=jsonpath='{.data.key}'
```

Store this key securely — without it, the etcd data cannot be decrypted on restore.

## Restoring an Etcd Snapshot

An etcd snapshot can currently only be restored during cluster creation. This is done by modifying the HostedCluster spec to include a `restoreSnapshotURL`.

### Step 1: Create a Pre-Signed URL

Generate a pre-signed URL so the etcd deployment can download the snapshot without credentials:

```bash
ETCD_SNAPSHOT="s3://${BUCKET_NAME}/${CLUSTER_NAME}-snapshot.db"
ETCD_SNAPSHOT_URL=$(aws s3 presign ${ETCD_SNAPSHOT})
```

### Step 2: Modify the HostedCluster Spec

Add the `restoreSnapshotURL` to the etcd section of the HostedCluster spec:

```yaml
spec:
  etcd:
    managed:
      storage:
        persistentVolume:
          size: 4Gi
        type: PersistentVolume
        restoreSnapshotURL:
        - "${ETCD_SNAPSHOT_URL}"
    managementType: Managed
```

### Step 3: Ensure Encryption Key Matches

The secret referenced from `spec.secretEncryption.aescbc` must contain the same AES key that was saved during the backup step. Without the correct key, the restored etcd data will be unreadable.
