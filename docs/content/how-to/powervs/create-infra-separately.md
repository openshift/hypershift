---
title: Create PowerVS Infra resources separately
---

# Create IBMCloud PowerVS Infra resources separately

The default behavior of the `hypershift create cluster powervs` command is to create cloud infrastructure
along with the manifests for hosted cluster and apply it. 

It is possible to create the cloud infrastructure separately so that the `hypershift create cluster powervs` command can just be used to create the manifests and apply it.

In order to do this, you need to:
1. [Create PowerVS infrastructure](#creating-the-powervs-infra) 
2. [Create cluster](#creating-the-cluster)

## Creating the PowerVS infra

Please see [prerequisites](prerequisites-and-env-guide.md) before setting up the infra

Use the `hypershift create infra powervs` command:

    CLUSTER_NAME=example
    INFRA_ID=example-infra
    REGION=tok
    ZONE=tok04
    VPC_REGION=jp-tok
    BASEDOMAIN=hypershift-on-power.com
    RESOURCE_GROUP=ibm-hypershift-dev
    OUTPUT_INFRA_FILE=infra.json

    ./bin/hypershift create infra powervs \
        --name $CLUSTER_NAME \
        --infra-id $INFRA_ID \
        --region $REGION \
        --zone $ZONE \
        --region $VPC_REGION \
        --base-domain $BASEDOMAIN \
        --resource-group $RESOURCE_GROUP \
        --output-file $OUTPUT_INFRA_FILE

where

* CLUSTER_NAME is a name for the cluster.
* INFRA_ID is a unique name that will be used to name the infrastructure resources.
* REGION is the region where you want to create the PowerVS resources.
* ZONE is the zone under POWERVS_REGION where you want to create the PowerVS resources.
* VPC_REGION is the region where you want to create the VPC resources.
* BASEDOMAIN is the CIS base domain that will be used for your hosted cluster's ingress. It should be an existing CIS domain name.
* RESOURCE_GROUP is the resource group in IBM Cloud where your infrastructure resources will be created.
* OUTPUT_INFRA_FILE is the file where IDs of the infrastructure that has been created will be stored in JSON format.
  This file can then be used as input to the `hypershift create cluster powervs` command to populate
  the appropriate fields in the HostedCluster and NodePool resources.


Running this command should result in the following resources getting created in IBM Cloud:

### PowerVS Cluster Infra Resources 

* 1 VPC with Subnet
* 1 PowerVS Cloud Instance
* 1 DHCP Service
  * 1 DHCP Private Network
  * 1 DHCP Public Network
* 1 Cloud Connection

## Creating the Cluster

Once you have the `OUTPUT_INFRA_FILE` generated, can pass this file `hypershift create cluster powervs` to this command with `--infra-json` flag
Running the below command set up the cluster on infra created separately

E.g.:

    CLUSTER_NAME=example
    REGION=tok
    ZONE=tok04
    VPC_REGION=jp-tok
    BASEDOMAIN=hypershift-on-power.com
    RESOURCE_GROUP=ibm-hypershift-dev
    RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release:4.12.0-0.nightly-multi-2022-09-08-131900
    PULL_SECRET="$HOME/pull-secret"
    INFRA_JSON=infra.json
    
    ./bin/hypershift create cluster powervs \
        --name $CLUSTER_NAME \
        --region $REGION \
        --zone $ZONE \
        --vpc-region $VPC_REGION \
        --base-domain $BASEDOMAIN \
        --resource-group $RESOURCE_GROUP \
        --release-image $RELEASE_IMAGE
        --pull-secret $PULL_SECRET \
        --infra-json $INFRA_JSON \
        --node-pool-replicas=2


!!! important

        Need to understand --recreate-secrets flag usage before using it. Enabling this flag will result in recreating the creds mentioned here [PowerVSPlatformSpec](https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io/v1alpha1.PowerVSPlatformSpec)

        This is required when rerunning `hypershift create cluster powervs` command, since API Key once created cannot be retrieved again.

        Please make sure cluster name used is unique across different management cluster before using this flag since this will result in removing the existing cred's service ID and recreate them.
