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

## Backup

This section complains interaction among multiple components. We will need to backup all the relevant things to raise up this same cluster in our target management cluster.

To do that we will: 

<details>
<summary><b>1</b>. Mark the Hosted Cluster with a ConfigMap which will declare the source Management Cluster it comes from. This is not mandatory but useful.</summary>

```bash
oc create configmap  -n default --from-literal=from=${MGMT_CLUSTER_NAME}
```

</details>
