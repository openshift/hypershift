---
title: Migrating Hosted Cluster among the same AWS Region
---



# Migrating Hosted Cluster among the same AWS Region

The way that a Hosted Cluster migration follows, it's basically done in 3 phases:

1 - Backup
2 - Restoration
3 - Teardown

Let's setup the environment to start the migration with our first cluster.

## Environment and Context

Our scenario involves 3 Clusters, 2 Management ones and 1 HostedCluster, which will be migrated. Depending on the situation we would like to migrate just the ControlPlane or the Controlplane + nodes.

These are the relevant data we need to know in order to migrate a cluster:

- **Source MGMT Namespace**: Source Management Namespace  
- **Source MGMT ClusterName**: Source Management Cluster Name
- **Source MGMT Kubeconfig**: Source Management Kubeconfig
- **Destination MGMT Kubeconfig**: Destination Management Kubeconfig
- **HC Kubeconfig**: Hosted Cluster Kubeconfig
- **SSH Key File**: SSH Public Key
- **Pull Secret**: Pull Secret file to access to the Release Images
- **AWS Credentials**: AWS Credentials file
- **AWS Region**: AWS Region
- **Base Domain**: DNS Base Domain to use it as external DNS.
- **S3 Bucket Name**: This is the bucket in the same **AWS Region** where the ETCD backup will be uploaded

These are the Variables we will use in the scripts:

<details>
<summary>Environment Variables</summary>

- Ensure all the file it's correct regarding you folder tree and put this env file in your filesystem, then execute `source env_file` from a terminal

```bash
SSH_KEY_FILE=${HOME}/.ssh/id_rsa.pub
BASE_PATH=${HOME}/hypershift
BASE_DOMAIN="aws.sample.com"
PULL_SECRET_FILE="${HOME}/pull_secret.json"
AWS_CREDS="${HOME}/.aws/credentials"
CONTROL_PLANE_AVAILABILITY_POLICY=SingleReplica
HYPERSHIFT_PATH=${BASE_PATH}/src/hypershift
HYPERSHIFT_CLI=${HYPERSHIFT_PATH}/bin/hypershift
HYPERSHIFT_IMAGE=${HYPERSHIFT_IMAGE:-"quay.io/${USER}/hypershift:latest"}
NODE_POOL_REPLICAS=${NODE_POOL_REPLICAS:-2}

# MGMT Context
MGMT_REGION=us-west-1
MGMT_CLUSTER_NAME="${USER}-dev"
MGMT_CLUSTER_NS=${USER}
MGMT_CLUSTER_DIR="${BASE_PATH}/hosted_clusters/${MGMT_CLUSTER_NS}-${MGMT_CLUSTER_NAME}"
MGMT_KUBECONFIG="${MGMT_CLUSTER_DIR}/kubeconfig"

# MGMT2 Context
MGMT2_CLUSTER_NAME="${USER}-dest"
MGMT2_CLUSTER_NS=${USER}
MGMT2_CLUSTER_DIR="${BASE_PATH}/hosted_clusters/${MGMT2_CLUSTER_NS}-${MGMT2_CLUSTER_NAME}"
MGMT2_KUBECONFIG="${MGMT2_CLUSTER_DIR}/kubeconfig"

# Hosted Cluster Context
HC_CLUSTER_NS=clusters
HC_REGION=us-west-1
HC_CLUSTER_NAME="${USER}-hosted"
HC_CLUSTER_DIR="${BASE_PATH}/hosted_clusters/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}"
HC_KUBECONFIG="${HC_CLUSTER_DIR}/kubeconfig"

BUCKET_NAME="${USER}-hosted-${MGMT_REGION}"
```

</details>


## Backup

This section complains interaction among multiple components. We will need to backup all the relevant things to raise up this same cluster in our target management cluster.

To do that we will: 

1. Mark the Hosted Cluster with a ConfigMap which will declare the source Management Cluster it comes from (This is not mandatory but useful).

    ```bash
    oc create configmap mgmt-parent-cluster -n default --from-literal=from=${MGMT_CLUSTER_NAME}
    ```

2. Shutdown the reconciliation in the HostedCluster we want to migrate and also in the Nodepools.


    <details>
    <summary>ControlPlane Migration</summary>
    
    ```bash
    PAUSED_UNTIL="true"
    oc patch -n ${HC_CLUSTER_NS} hostedclusters/${HC_CLUSTER_NAME} -p '{"spec":{"pausedUntil":"'${PAUSED_UNTIL}'"}}' --type=merge
    oc scale deployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 kube-apiserver openshift-apiserver openshift-oauth-apiserver control-plane-operator
    ```
    
    </details>

    <details>
    <summary>ControlPlane + NodePool Migration</summary>
    
    ```bash
    PAUSED_UNTIL="true"
    oc patch -n ${HC_CLUSTER_NS} hostedclusters/${HC_CLUSTER_NAME} -p '{"spec":{"pausedUntil":"'${PAUSED_UNTIL}'"}}' --type=merge
    oc patch -n ${HC_CLUSTER_NS} nodepools/${NODEPOOLS} -p '{"spec":{"pausedUntil":"'${PAUSED_UNTIL}'"}}' --type=merge
    oc scale deployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 kube-apiserver openshift-apiserver openshift-oauth-apiserver control-plane-operator
    ```
    
    </details>

3. Backup ETCD and Upload to S3 Bucket

To do this programatically it's a but more complicated, but we will try to put all the necessary steps in a bash script 

<details>
<summary>ETCD Backup and Upload to S3 procedure</summary>


    - As an advice, we recommend to wrap it up in a function and call it from the main function.

    ```bash 
    # ETCD Backup
    ETCD_PODS="etcd-0"
    if [ "${CONTROL_PLANE_AVAILABILITY_POLICY}" = "HighlyAvailable" ]; then
      ETCD_PODS="etcd-0 etcd-1 etcd-2"
    fi
    
    for POD in ${ETCD_PODS}; do
      # Create an etcd snapshot
      oc exec -it ${POD} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -- env ETCDCTL_API=3 /usr/bin/etcdctl --cacert /etc/etcd/tls/client/etcd-client-ca.crt --cert /etc/etcd/tls/client/etcd-client.crt --key /etc/etcd/tls/client/etcd-client.key --endpoints=localhost:2379 snapshot save /var/lib/data/snapshot.db
      oc exec -it ${POD} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -- env ETCDCTL_API=3 /usr/bin/etcdctl -w table snapshot status /var/lib/data/snapshot.db
    
      FILEPATH="/${BUCKET_NAME}/${HC_CLUSTER_NAME}-${POD}-snapshot.db"
      CONTENT_TYPE="application/x-compressed-tar"
      DATE_VALUE=`date -R`
      SIGNATURE_STRING="PUT\n\n${CONTENT_TYPE}\n${DATE_VALUE}\n${FILEPATH}"
    
      set +x
      ACCESS_KEY=$(grep aws_access_key_id ${AWS_CREDS} | head -n1 | cut -d= -f2 | sed "s/ //g")
      SECRET_KEY=$(grep aws_secret_access_key ${AWS_CREDS} | head -n1 | cut -d= -f2 | sed "s/ //g")
      SIGNATURE_HASH=$(echo -en ${SIGNATURE_STRING} | openssl sha1 -hmac "${SECRET_KEY}" -binary | base64)
      set -x
    
      # FIXME: this is pushing to the OIDC bucket
      oc exec -it etcd-0 -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -- curl -X PUT -T "/var/lib/data/snapshot.db" \
        -H "Host: ${BUCKET_NAME}.s3.amazonaws.com" \
        -H "Date: ${DATE_VALUE}" \
        -H "Content-Type: ${CONTENT_TYPE}" \
        -H "Authorization: AWS ${ACCESS_KEY}:${SIGNATURE_HASH}" \
        https://${BUCKET_NAME}.s3.amazonaws.com/${HC_CLUSTER_NAME}-${POD}-snapshot.db
    done
   ```


</details>
