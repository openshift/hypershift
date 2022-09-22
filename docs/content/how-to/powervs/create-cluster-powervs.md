---
title: Create PowerVS Hypershift Cluster
---

# Create PowerVS Hypershift Cluster

Creating Hypershift cluster in IBM Cloud PowerVS service.

## Prerequisites

Please see [prerequisites](../prerequisites-and-env-guide.md/#prerequisites) before setting up the cluster

## Creating the Cluster

Use the `hypershift create cluster powervs` command:

    CLUSTER_NAME=example
    REGION=tok
    ZONE=tok04
    VPC_REGION=jp-tok
    BASEDOMAIN=hypershift-on-power.com
    RESOURCE_GROUP=ibm-hypershift-dev
    RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release:4.12.0-0.nightly-multi-2022-09-08-131900
    PULL_SECRET="$HOME/pull-secret"
    
    ./bin/hypershift create cluster powervs \
        --name $CLUSTER_NAME \
        --region $REGION \
        --zone $ZONE \
        --vpc-region $VPC_REGION \
        --base-domain $BASEDOMAIN \
        --resource-group $RESOURCE_GROUP \
        --release-image $RELEASE_IMAGE
        --pull-secret $PULL_SECRET \
        --node-pool-replicas=2

where

* CLUSTER_NAME is a name for the cluster.
* REGION is the region where you want to create the powervs resources.
* ZONE is the zone under REGION where you want to create the powervs resources.
* VPC_REGION is the region where you want to create the vpc resources.
* BASEDOMAIN is the CIS base domain that will be used for your hosted cluster's ingress. It should be an existing CIS domain name.
* RESOURCE_GROUP is the resource group in IBMCloud where your infrastructure resources will be created.
* RELEASE_IMAGE is the latest multi arch release image.
* PULL_SECRET is a file that contains a valid OpenShift pull secret.
* node-pool-replicas is worker node count 

Running this command will create [infra](../create-infra-powervs-separately.md/#powevs-cluster-infra-resources ) for the Hypershift cluster and will create HostedCluster and NodePool spec and deploys it.