---
title: Create PowerVS Hypershift Cluster
---

# Create PowerVS Hypershift Cluster

Creating Hypershift cluster in IBM Cloud PowerVS service.

## Prerequisites

Please see [prerequisites](./prerequisites-and-env-guide.md/#prerequisites) before setting up the cluster

## Creating the Cluster

Use the `hypershift create cluster powervs` command:

    ./bin/hypershift create cluster powervs --base-domain BASEDOMAIN
        --resource-group RESOURCE_GROUP \
        --pull-secret PULL_SECRET \
        --region POWERVS_REGION \
        --zone POWERVS_ZONE \
        --vpc-region VPC_REGION \
        --node-pool-replicas=2

E.g.:

      ./bin/hypershift create cluster powervs --base-domain scnl-ibm.com \
      --resource-group hypershift-resource-group \
      --pull-secret ./pull-secret \
      --region tok \
      --zone tok04 \
      --vpc-region jp-tok \
      --node-pool-replicas=2

where

* BASEDOMAIN is the CIS base domain that will be used for your hosted cluster's ingress. It should be an existing CIS domain name.
* RESOURCE_GROUP is the resource group in IBMCloud where your infrastructure resources will be created.
* PULL_SECRET is a file that contains a valid OpenShift pull secret.
* POWERVS_REGION is the region where you want to create the powervs resources.
* POWERVS_ZONE is the zone under POWERVS_REGION where you want to create the powervs resources.
* VPC_REGION is the region where you want to create the vpc resources.
* node-pool-replicas is worker node count 

Running this command will create [infra](./create-infra-powervs-separately.md/#powevs-cluster-infra-resources ) for the Hypershift cluster and will create HostedCluster and NodePool spec and deploys it.

You can create infra separately and use it to create Hypershift cluster which reduces the infra creation time.