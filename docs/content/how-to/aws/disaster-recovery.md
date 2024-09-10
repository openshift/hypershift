---
title: Disaster Recovery
---

# Disaster Recovery

## Migrating Hosted Cluster within the same AWS Region

### Use cases for Disaster Recovery

This procedure **is helpful** for:

1. *The control-plane is down for your hosted cluster (api-server, etcd,...)*
2. *Hypershift operator is not working, it’s down and it cannot be recovered*

This procedure **is not helpful** for:

1. *My compute nodes are frozen or not working fine*
    - For this situation you will need to access the serial console of the node in order to see what is happening.

2. *The management cluster API-server/etcd is down?*
    - For this second situation it does not make sense to use this procedure, so you can use a backup/recovery tool like Velero in order to recover the Etcd.

!!! note Important
    Other situations should be carefully examined, to ensure the stability of the other deployed HostedClusters

!!! warning
    These are some examples where this procedure could be useful. We don't recommend following this procedure unless it is strictly necessary.

### Preface and Considerations

The behaviour of this implementation is focused on the transparency for the user. Hypershift will not disrupt any customer workloads at anytime, also have in mind that the service workloads will be up and running during the migration process. Maybe at some point the Cluster API will be down but this will not affect the services running on the worker nodes.

In the storage side, It's mandatory to have under consideration that when we move a HostedControlPlane to another Management cluster we use some external services, which allows the migration to make it happen. We reusethe storage provisioned in AWS by the initial ControlPlane (PVs/PVCs) in the destination Management cluster.

Regarding the Workers nodes assigned to the cluster, during the migration they will still point to the same DNS entry, and we, under the hood, change the DNS Records to point to the new Management Cluster API, that way the node migration is transparent for the user.

!!! important
    Keep in mind that this **"migration"** capability is only for **disaster recovery purposes**, please **DO NOT** use this to perform clusters migrations as a common task in your platform.

These next arguments depend on how the Hypershift Operator has been deployed and how a Hosted Cluster has been created. E.G If we want to go ahead with the procedure and our cluster is **private** we need to make sure that our **Hypershift Operator** has been deployed with the arguments set in the **Private** tab for **Hypershift Operator Deployment access endpoints arguments** and our **Hosted Cluster** has been created using the arguments following the **Private** tab in the **Arguments of the CLI when creating a HostedCluster** section down below.

!!! warning
    Since this is a disaster recovery procedure, unexpected things could happen because of all the moving components involved. To assist, see this [troubleshooting section](./troubleshooting/troubleshooting-disaster-recovery.md) for the most common issues identified.

- Hypershift Operator Deployment endpoint access arguments

=== "**Public** and **PublicAndPrivate**"

    ```bash
    --external-dns-provider=aws \
    --external-dns-credentials=<AWS Credentials location> \
    --external-dns-domain-filter=<External DNS for HostedCluster>
    ```

=== "**Private**"

    ```bash
    --private-platform aws \
    --aws-private-creds <Path to AWS Credentials> \
    --aws-private-region <AWS Region>
    ```

- Arguments of the CLI when creating a HostedCluster

=== "**Public**"

    ```bash
    --external-dns-domain=<External DNS Domain> \
    --endpoint-access=Public
    ```

=== "**PublicAndPrivate**"

    ```bash
    --external-dns-domain=<External DNS Domain> \
    --endpoint-access=PublicAndPrivate
    ```


=== "**Private**"

    ```bash
    --endpoint-access=Private
    ```

This way, the server URL will end in something like this: "https://api-sample-hosted.sample-hosted.aws.openshift.com"

The way that a Hosted Cluster migration follows, it's basically done in 3 phases:

1. **Backup**
2. **Restoration**
3. **Teardown**

Let's setup the environment to start the migration with our first cluster.

### Environment and Context

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
<summary>Sample Environment Variables</summary>

- Ensure all the file it's correct regarding you folder tree and put this env file in your filesystem, then execute `source env_file` from a terminal

```bash
SSH_KEY_FILE=${HOME}/.ssh/id_rsa.pub
BASE_PATH=${HOME}/hypershift
BASE_DOMAIN="aws.sample.com"
PULL_SECRET_FILE="${HOME}/pull_secret.json"
AWS_CREDS="${HOME}/.aws/credentials"
AWS_ZONE_ID="Z02718293M33QHDEQBROL"

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
BACKUP_DIR=${HC_CLUSTER_DIR}/backup

BUCKET_NAME="${USER}-hosted-${MGMT_REGION}"

# DNS
AWS_ZONE_ID="Z07342811SH9AA102K1AC"
EXTERNAL_DNS_DOMAIN="hc.jpdv.aws.kerbeross.com"
```

</details>

And this is how the Migration workflow will happen

![gif](../../images/hc-migration-workflow.gif)


### Backup

This section complains interaction among multiple components. We will need to backup all the relevant things to raise up this same cluster in our target management cluster.

To do that we will:

1. Mark the Hosted Cluster with a ConfigMap which will declare the source Management Cluster it comes from (This is not mandatory but useful).

<details>
<summary>Config Map creation to set the Source Management Cluster</summary>


```bash
oc create configmap mgmt-parent-cluster -n default --from-literal=from=${MGMT_CLUSTER_NAME}
```

</details>

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

The whole process of this step is documented [here](https://hypershift-docs.netlify.app/how-to/aws/etc-backup-restore/), even with that we will go through the process in a more programmatically way.

To do this programmatically it's a bit more complicated, but we will try to put all the necessary steps in a bash script

<details>
<summary>ETCD Backup and Upload to S3 procedure</summary>

- As an advice, we recommend to wrap it up in a function and call it from the main function.

```bash
# ETCD Backup
ETCD_PODS="etcd-0"
if [ "${CONTROL_PLANE_AVAILABILITY_POLICY}" = "HighlyAvailable" ]; then
  ETCD_PODS="etcd-0 etcd-1 etcd-2"
fi

## If you are in 4.12 or above, use this one
ETCD_CA_LOCATION=/etc/etcd/tls/etcd-ca/ca.crt

## If you are in 4.11 or below, use this other one
#ETCD_CA_LOCATION=/etc/etcd/tls/client/etcd-client-ca.crt

for POD in ${ETCD_PODS}; do
  # Create an etcd snapshot
  oc exec -it ${POD} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -- env ETCDCTL_API=3 /usr/bin/etcdctl --cacert ${ETCD_CA_LOCATION} --cert /etc/etcd/tls/client/etcd-client.crt --key /etc/etcd/tls/client/etcd-client.key --endpoints=localhost:2379 snapshot save /var/lib/data/snapshot.db

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

!!! warning Warning
    The CA Certificate of ETCD has changed the location in 4.12, so take care about the command execution because it will fail. It's safe to reexecute this piece of code, it just will backup the ETCD in S3. In order to know which version you have installed, just execute this command `oc version -o json | jq -e .openshiftVersion`


4. Backup Kubernetes/Openshift objects

    - From HostedCluster Namespace:
        - HostedCluster and NodePool Objects
        - HostedCluster Secrets
    - From Hosted Control Plane Namespace:
        - HostedControlPlane
        - Cluster
        - AWSCluster, AWSMachineTemplate, AWSMachine
        - MachineDeployments, MachineSets and Machines
        - ControlPlane Secrets

<details>
<summary>Openshift Objects backup</summary>

```bash
mkdir -p ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS} ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
chmod 700 ${BACKUP_DIR}/namespaces/

# HostedCluster
echo "Backing Up HostedCluster Objects:"
oc get hc ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}.yaml
echo "--> HostedCluster"
sed -i '' -e '/^status:$/,$d' ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}.yaml

# NodePool
oc get np ${NODEPOOLS} -n ${HC_CLUSTER_NS} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/np-${NODEPOOLS}.yaml
echo "--> NodePool"
sed -i '' -e '/^status:$/,$ d' ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/np-${NODEPOOLS}.yaml

# Secrets in the HC Namespace
echo "--> HostedCluster Secrets:"
for s in $(oc get secret -n ${HC_CLUSTER_NS} | grep "^${HC_CLUSTER_NAME}" | awk '{print $1}'); do
    oc get secret -n ${HC_CLUSTER_NS} $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/secret-${s}.yaml
done

# Secrets in the HC Control Plane Namespace
echo "--> HostedCluster ControlPlane Secrets:"
for s in $(oc get secret -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} | egrep -v "docker|service-account-token|oauth-openshift|NAME|token-${HC_CLUSTER_NAME}" | awk '{print $1}'); do
    oc get secret -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/secret-${s}.yaml
done

# Hosted Control Plane
echo "--> HostedControlPlane:"
oc get hcp ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/hcp-${HC_CLUSTER_NAME}.yaml

# Cluster
echo "--> Cluster:"
CL_NAME=$(oc get hcp ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o jsonpath={.metadata.labels.\*} | grep ${HC_CLUSTER_NAME})
oc get cluster ${CL_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/cl-${HC_CLUSTER_NAME}.yaml

# AWS Cluster
echo "--> AWS Cluster:"
oc get awscluster ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/awscl-${HC_CLUSTER_NAME}.yaml

# AWS MachineTemplate
echo "--> AWS Machine Template:"
oc get awsmachinetemplate ${NODEPOOLS} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/awsmt-${HC_CLUSTER_NAME}.yaml

# AWS Machines
echo "--> AWS Machine:"
CL_NAME=$(oc get hcp ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o jsonpath={.metadata.labels.\*} | grep ${HC_CLUSTER_NAME})
for s in $(oc get awsmachines -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --no-headers | grep ${CL_NAME} | cut -f1 -d\ ); do
    oc get -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} awsmachines $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/awsm-${s}.yaml
done

# MachineDeployments
echo "--> HostedCluster MachineDeployments:"
for s in $(oc get machinedeployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name); do
    mdp_name=$(echo ${s} | cut -f 2 -d /)
    oc get -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/machinedeployment-${mdp_name}.yaml
done

# MachineSets
echo "--> HostedCluster MachineSets:"
for s in $(oc get machineset -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name); do
    ms_name=$(echo ${s} | cut -f 2 -d /)
    oc get -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/machineset-${ms_name}.yaml
done

# Machines
echo "--> HostedCluster Machine:"
for s in $(oc get machine -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name); do
    m_name=$(echo ${s} | cut -f 2 -d /)
    oc get -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/machine-${m_name}.yaml
done
```

</details>

5. Cleanup the ControlPlane Routes (only in `PublicAndPrivate` and `Public` clusters)

    - This will allow the **ExternalDNS Operator** to delete the Route53 entries in AWS and they will not be recreated because of this HostedCluster it's paused.

<details>
<summary>HostedCluster ControlPlane Routes Cleanup</summary>

- Just to clean the routes you could execute this command, but you will need to wait until the Route53 are clean (this is why I will add an alternative to validate this step).

```bash
oc delete routes -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --all
```

- (Alternative bash script) Cleanup OCP HC ControlPlane Routes and wait until Route53 it's clean (only in `PublicAndPrivate` and `Public` clusters)
```bash
function clean_routes() {

    if [[ -z "${1}" ]];then
        echo "Give me the NS where to clean the routes"
        exit 1
    fi

    # Constants
    if [[ -z "${2}" ]];then
        echo "Give me the Route53 zone ID"
        exit 1
    fi

    ZONE_ID=${2}
    ROUTES=10
    timeout=40
    count=0

    # This allows us to remove the ownership in the AWS for the API route
    oc delete route -n ${1} --all

    while [ ${ROUTES} -gt 2 ]
    do
        echo "Waiting for ExternalDNS Operator to clean the DNS Records in AWS Route53 where the zone id is: ${ZONE_ID}..."
        echo "Try: (${count}/${timeout})"
        sleep 10
        if [[ $count -eq timeout ]];then
            echo "Timeout waiting for cleaning the Route53 DNS records"
            exit 1
        fi
        count=$((count+1))
        ROUTES=$(aws route53 list-resource-record-sets --hosted-zone-id ${ZONE_ID} --max-items 10000 --output json | grep -c ${EXTERNAL_DNS_DOMAIN})
    done
}

# SAMPLE: clean_routes "<HC ControlPlane Namespace>" "<AWS_ZONE_ID>"
clean_routes "${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}" "${AWS_ZONE_ID}"
```

</details>

!!! warning Warning
    This step is only relevant if you have a HostedCluster with a `--endpoint-access` argument as `PublicAndPrivate` or `Public`. If that's not the case, you will not have the need to execute this part.

This was the last step on Backup stage, now we encourage you to validate all the OCP Objects and the S3 Bucket in order to ensure all is fine.

### Restoration

This step it's basically catch all the objects which has been backuped up and restore them in the Destination Management Cluster.

!!! note
    Ensure you have the destination's cluster's Kubeconfig placed as is set in `MGMT2_KUBECONFIG` (if you follow the final script) or `KUBECONFIG` variable if you are going step by step. `export KUBECONFIG=${MGMT2_KUBECONFIG}` or `export KUBECONFIG=<Kubeconfig FilePath>`

1. Ensure you don't have an old Namespace in the new MGMT Cluster with the same name as the cluster that are you migrating.

<details>
<summary>Delete the Namespace that will be used by the Migrated Cluster and the Control plane</summary>

```bash
# Just in case
export KUBECONFIG=${MGMT2_KUBECONFIG}
BACKUP_DIR=${HC_CLUSTER_DIR}/backup

# Namespace deletion in the destination Management cluster
oc delete ns ${HC_CLUSTER_NS} || true
oc delete ns ${HC_CLUSTER_NS}-{HC_CLUSTER_NAME} || true
```

</details>

2. ReCreate the deleted namespaces from fresh start

<details>
<summary>Namespace creation</summary>

```bash
# Namespace creation
oc new-project ${HC_CLUSTER_NS}
oc new-project ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
```

</details>

3. Restore Secrets in the HC Namespace

<details>
<summary>Secrets Restoration in HostedCluster Namespace</summary>

```bash
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/secret-*
```

</details>

4. Restore Objects in the HC ControlPlane Namespace

<details>
<summary>Restore OCP Objects related with the HC ControlPlane</summary>

```bash
# Secrets
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/secret-*

# Cluster
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/hcp-*
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/cl-*
```

</details>

5. (Optional) Restore objects in the HC ControlPlane Namespace

    !!! note
        This step it's only relevant if you are migrating the Nodes and the NodePool to reuse the AWS Instances.

<details>
<summary>Restore OCP Nodes related objects within the HC ControlPlane Namespace</summary>

```bash
# AWS
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/awscl-*
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/awsmt-*
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/awsm-*

# Machines
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/machinedeployment-*
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/machineset-*
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/machine-*
```

</details>

6. Restore the ETCD Backup and HostedCluster

<details>
<summary>Bash script to restore ETCD and HostedCluster object</summary>

```bash
ETCD_PODS="etcd-0"
if [ "${CONTROL_PLANE_AVAILABILITY_POLICY}" = "HighlyAvailable" ]; then
  ETCD_PODS="etcd-0 etcd-1 etcd-2"
fi

HC_RESTORE_FILE=${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}-restore.yaml
HC_BACKUP_FILE=${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}.yaml
HC_NEW_FILE=${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}-new.yaml
cat ${HC_BACKUP_FILE} > ${HC_NEW_FILE}
cat > ${HC_RESTORE_FILE} <<EOF
    restoreSnapshotURL:
EOF

for POD in ${ETCD_PODS}; do
  # Create a pre-signed URL for the etcd snapshot
  ETCD_SNAPSHOT="s3://${BUCKET_NAME}/${HC_CLUSTER_NAME}-${POD}-snapshot.db"
  ETCD_SNAPSHOT_URL=$(AWS_DEFAULT_REGION=${MGMT2_REGION} aws s3 presign ${ETCD_SNAPSHOT})

  # FIXME no CLI support for restoreSnapshotURL yet
  cat >> ${HC_RESTORE_FILE} <<EOF
    - "${ETCD_SNAPSHOT_URL}"
EOF
done

cat ${HC_RESTORE_FILE}

if ! grep ${HC_CLUSTER_NAME}-snapshot.db ${HC_NEW_FILE}; then
  sed -i '' -e "/type: PersistentVolume/r ${HC_RESTORE_FILE}" ${HC_NEW_FILE}
  sed -i '' -e '/pausedUntil:/d' ${HC_NEW_FILE}
fi

HC=$(oc get hc -n ${HC_CLUSTER_NS} ${HC_CLUSTER_NAME} -o name || true)
if [[ ${HC} == "" ]];then
    echo "Deploying HC Cluster: ${HC_CLUSTER_NAME} in ${HC_CLUSTER_NS} namespace"
    oc apply -f ${HC_NEW_FILE}
else
    echo "HC Cluster ${HC_CLUSTER_NAME} already exists, avoiding step"
fi
```

</details>

7. (Optional) Restore the NodePool

    !!! note
        This step it's only relevant if you are migrating the Nodes and the NodePool to reuse the AWS Instances.

<details>
<summary>Restore the NodePool object</summary>

```bash
oc apply -f ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/np-*
```

</details>

This was our last step in the **Restoration** phase. If you are not migrating nodes, congratulations, you can pass to the next section **Teardown**.


(Optional) Now we will need to wait for some time until the Nodes gets fully migrated. We recommend to use this function

<details>
<summary>Ensure Nodes Migrated</summary>

```bash
timeout=40
count=0
NODE_STATUS=$(oc get nodes --kubeconfig=${HC_KUBECONFIG} | grep -v NotReady | grep -c "worker") || NODE_STATUS=0

while [ ${NODE_POOL_REPLICAS} != ${NODE_STATUS} ]
do
    echo "Waiting for Nodes to be Ready in the destination MGMT Cluster: ${MGMT2_CLUSTER_NAME}"
    echo "Try: (${count}/${timeout})"
    sleep 30
    if [[ $count -eq timeout ]];then
        echo "Timeout waiting for Nodes in the destination MGMT Cluster"
        exit 1
    fi
    count=$((count+1))
    NODE_STATUS=$(oc get nodes --kubeconfig=${HC_KUBECONFIG} | grep -v NotReady | grep -c "worker") || NODE_STATUS=0
done
```

</details>

### Teardown

In this section we will shutdown and delete the HostedCluster in the source Management Cluster.
!!! note
    Ensure you have the source's cluster's Kubeconfig placed as is set in `MGMT_KUBECONFIG` (if you follow the final script) or `KUBECONFIG` variable if you are going step by step. `export KUBECONFIG=${MGMT_KUBECONFIG}` or `export KUBECONFIG=<Kubeconfig FilePath>`

1. Scale The Deployments and StatefulSets

<details>
<summary>ScaleDown Pod relevant objects in the HC ControlPlane Namespace</summary>

```bash
# Just in case
export KUBECONFIG=${MGMT_KUBECONFIG}

# Scale down deployments
oc scale deployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 --all
oc scale statefulset.apps -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 --all
sleep 15
```

</details>

2. Delete the NodePool objects

<details>
<summary>Delete NodePools</summary>

```bash
NODEPOOLS=$(oc get nodepools -n ${HC_CLUSTER_NS} -o=jsonpath='{.items[?(@.spec.clusterName=="'${HC_CLUSTER_NAME}'")].metadata.name}')
if [[ ! -z "${NODEPOOLS}" ]];then
    oc patch -n "${HC_CLUSTER_NS}" nodepool ${NODEPOOLS} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]'
    oc delete np -n ${HC_CLUSTER_NS} ${NODEPOOLS}
fi
```

</details>

3. Delete the Machines and MachineSets

<details>
<summary>Delete Machines and MachineSets</summary>

```bash
# Machines
for m in $(oc get machines -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name); do
    oc patch -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${m} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]' || true
    oc delete -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${m} || true
done

oc delete machineset -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --all || true
```

</details>

4. Delete Cluster object

<details>
<summary>Delete the Cluster</summary>

```bash
# Cluster
C_NAME=$(oc get cluster -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name)
oc patch -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${C_NAME} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]'
oc delete cluster.cluster.x-k8s.io -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --all
```

</details>

5. Delete the AWS Machines (Kubernetes Objects)

!!! note
    Don't worry about the real AWS Machines, even if you delete this object, the CAPI controllers are down and will not affect the cloud instances


<details>
<summary>Delete AWS Machines OCP Objects</summary>

```bash
# AWS Machines
for m in $(oc get awsmachine.infrastructure.cluster.x-k8s.io -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name)
do
    oc patch -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${m} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]' || true
    oc delete -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${m} || true
done
```

</details>

6. Delete HostedControlPlane and Controlplane HC Namespace

<details>
<summary>Delete HostedControlPlane and ControlPlane HC Namespace objects</summary>

```bash
# Delete HCP and ControlPlane HC NS
oc patch -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} hostedcontrolplane.hypershift.openshift.io ${HC_CLUSTER_NAME} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]'
oc delete hostedcontrolplane.hypershift.openshift.io -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --all
oc delete ns ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} || true
```

</details>

7. Delete the HostedCluster and HC Namespace

<details>
<summary>Delete the HostedCluster object</summary>

```bash
# Delete HC and HC Namespace
oc -n ${HC_CLUSTER_NS} patch hostedclusters ${HC_CLUSTER_NAME} -p '{"metadata":{"finalizers":null}}' --type merge || true
oc delete hc -n ${HC_CLUSTER_NS} ${HC_CLUSTER_NAME}  || true
oc delete ns ${HC_CLUSTER_NS} || true
```

</details>

And that was it, following this whole process you could migrate an HostedCluster from one Management Cluster to other one in the same AWS Region.

To ensure all is working fine, you just need to validate that all the objects are in the right place:

```bash
# Validations
export KUBECONFIG=${MGMT2_KUBECONFIG}

oc get hc -n ${HC_CLUSTER_NS}
oc get np -n ${HC_CLUSTER_NS}
oc get pod -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
oc get machines -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}

# Inside the HostedCluster
export KUBECONFIG=${HC_KUBECONFIG}
oc get clusterversion
oc get nodes
```

8. (Optional) Restart OVN Pods in compute nodes (only in `PublicAndPrivate` and `Public` clusters)
After the Teardown of the HostedCluster in the source Management Cluster **you will need to delete the OVN pods in the HostedCluster** in order to perform the connection with the new OVN Master running in the new Management Cluster.

To do that you just need to load the proper KUBECONFIG env var with the Hosted Cluster Kubeconfig path and execute this command:

```bash
oc delete pod -n openshift-ovn-kubernetes --all
```

with that, all the ClusterOperators that were failing and all the new pods generated, will get executed without issues.

!!! warning Warning
    This step is only relevant if you have a HostedCluster with a `--endpoint-access` argument as `PublicAndPrivate` or `Public`. If that's not the case, you will not have the need to execute this part.

### Migration Helper script

In order to ensure the that whole migration works fine, you could use this helper script that should work out of the box.

<details>
<summary>HC Migration Script</summary>

In order to execute the script, just:
- Fill the common variables and save the file as `../common/common.sh`
- Execute the migration script without params.

Now let's take a look to that script

- Common Variables

```bash
# Fill the Common variables to fit your environment, this is just a sample
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
MGMT2_REGION=us-west-1
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
BACKUP_DIR=${HC_CLUSTER_DIR}/backup

BUCKET_NAME="${USER}-hosted-${MGMT_REGION}"

# DNS
AWS_ZONE_ID="Z026552815SS3YPH9H6MG"
EXTERNAL_DNS_DOMAIN="guest.jpdv.aws.kerbeross.com"
```

- Migration Script
The migration script is maintained at https://github.com/openshift/hypershift/blob/main/contrib/migration/migrate-hcp.sh
</details>
