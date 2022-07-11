---
title: Etcd backup/restore (manual process)
---

# Etcd backup/restore (manual process)

It is possible to manually snapshot the etcd state for a HostedCluster, currently this
is a fully manual process and requires API downtime.

## Creating the Etcd snapshot

First we must [Pause reconciliation](../pause-reconciliation.md) of the HostedCluster:

    oc patch -n clusters hostedclusters/${CLUSTER_NAME} -p '{"spec":{"pausedUntil":"'${PAUSED_UNTIL}'"}}' --type=merge

Now stop all etcd-writer deployments:

    oc scale deployment -n ${HOSTED_CLUSTER_NAMESPACE} --replicas=0 kube-apiserver openshift-apiserver openshift-oauth-apiserver

You can now perform an etcd snapshot via `exec` in each etcd container:

    oc exec -it etcd-0 -n ${HOSTED_CLUSTER_NAMESPACE} -- env ETCDCTL_API=3 /usr/bin/etcdctl --cacert /etc/etcd/tls/client/etcd-client-ca.crt --cert /etc/etcd/tls/client/etcd-client.crt --key /etc/etcd/tls/client/etcd-client.key --endpoints=localhost:2379 snapshot save /var/lib/data/snapshot.db
    oc exec -it etcd-0 -n ${HOSTED_CLUSTER_NAMESPACE} -- env ETCDCTL_API=3 /usr/bin/etcdctl -w table snapshot status /var/lib/data/snapshot.db

The snapshot data can then be copied to some location where it can be later retrieved, for example S3:

    BUCKET_NAME=somebucket
    FILEPATH="/${BUCKET_NAME}/${CLUSTER_NAME}-snapshot.db"
    CONTENT_TYPE="application/x-compressed-tar"
    DATE_VALUE=`date -R`
    SIGNATURE_STRING="PUT\n\n${CONTENT_TYPE}\n${DATE_VALUE}\n${FILEPATH}"
    ACCESS_KEY=accesskey
    SECRET_KEY=secret
    SIGNATURE_HASH=`echo -en ${SIGNATURE_STRING} | openssl sha1 -hmac ${SECRET_KEY} -binary | base64`

    oc exec -it etcd-0 -n ${HOSTED_CLUSTER_NAMESPACE} -- curl -X PUT -T "/var/lib/data/snapshot.db" \
      -H "Host: ${BUCKET_NAME}.s3.amazonaws.com" \
      -H "Date: ${DATE_VALUE}" \
      -H "Content-Type: ${CONTENT_TYPE}" \
      -H "Authorization: AWS ${ACCESS_KEY}:${SIGNATURE_HASH}" \
      https://${BUCKET_NAME}.s3.amazonaws.com/${CLUSTER_NAME}-snapshot.db

If you wish to restore the snapshot on some new cluster later, ensure you have saved the encryption secret referenced from the HostedCluster resource, e.g:

    oc get hostedcluster $CLUSTER_NAME -o=jsonpath='{.spec.secretEncryption.aescbc}'
    {"activeKey":{"name":"CLUSTER_NAME-etcd-encryption-key"}}

    # Save this secret, or the key it contains so the etcd data can later be decrypted
    oc get secret ${CLUSTER_NAME}-etcd-encryption-key -o=jsonpath='{.data.key}'

## Restoring an etcd snapshot

An etcd snapshot can currently only be restored on cluster creation, this can be
achieved by modifying the output from `create cluster --render`, and defining a
`restoreSnapshotURL` in the etcd section of the HostedCluster spec.

First we must create a pre-signed URL so the previously saved etcd snapshot
can be downloaded from S3 without passing credentials to the etcd deployment,
with the `aws` CLI this can be achieved like:

    ETCD_SNAPSHOT=${ETCD_SNAPSHOT:-"s3://${BUCKET_NAME}/${CLUSTER_NAME}-snapshot.db"}
    ETCD_SNAPSHOT_URL=$(aws s3 presign ${ETCD_SNAPSHOT})

Now we modify the HostedCluster spec to refer to this URL, e.g:

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

Finally you must ensure the secret referenced from the `spec.secretEncryption.aescbc` contains
the same AES key saved in the previous steps.
