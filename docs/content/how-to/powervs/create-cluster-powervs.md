---
title: Create PowerVS Hosted Cluster
---

# Create PowerVS Hosted Cluster

Create Hosted cluster in IBM Cloud PowerVS service.

## Prerequisites

Please see [prerequisites](prerequisites-and-env-guide.md) before setting up the cluster

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
* REGION is the region where you want to create the PowerVS resources.
* ZONE is the zone under REGION where you want to create the PowerVS resources.
* VPC_REGION is the region where you want to create the VPC resources.
* BASEDOMAIN is the CIS base domain that will be used for your hosted cluster's ingress. It should be an existing CIS domain name.
* RESOURCE_GROUP is the resource group in IBM Cloud where your infrastructure resources will be created.
* RELEASE_IMAGE is the latest multi arch release image.
* PULL_SECRET is a file that contains a valid OpenShift pull secret.
* node-pool-replicas is worker node count 

Running this command will create [infra](create-infra-separately.md) and manifests for the hosted cluster and deploys it.

## Utilizing Power Edge Router(PER) via Transit Gateway
To use IBM Cloud's PER feature via transit gateway, need to pass `--use-power-edge-router` and `--transit-gateway-location $TRANSIT_GATEWAY_LOCATION` flags to create cluster command like below.

    TRANSIT_GATEWAY_LOCATION=us-south
    ./bin/hypershift create cluster powervs \
        --name $CLUSTER_NAME \
        --region $REGION \
        --zone $ZONE \
        --vpc-region $VPC_REGION \
        --base-domain $BASEDOMAIN \
        --resource-group $RESOURCE_GROUP \
        --release-image $RELEASE_IMAGE
        --pull-secret $PULL_SECRET \
        --node-pool-replicas=2 \
        --use-power-edge-router \
        --transit-gateway-location $TRANSIT_GATEWAY_LOCATION

Read [here](https://cloud.ibm.com/docs/power-iaas?topic=power-iaas-per) to know more about PER and data centers where its deployed currently.

!!! important

        Need to understand --recreate-secrets flag usage before using it. Enabling this flag will result in recreating the creds mentioned here [PowerVSPlatformSpec](https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io/v1alpha1.PowerVSPlatformSpec)

        This is required when rerunning `hypershift create cluster powervs` command, since API Key once created cannot be retrieved again.

        Please make sure cluster name used is unique across different management cluster before using this flag since this will result in removing the existing cred's service ID and recreate them.
