---
title: Create IBMCloud PowerVS Infra resources separately
---

# Create IBMCloud PowerVS Infra resources separately

The default behavior of the `hypershift create cluster powervs` command is to create cloud infrastructure
along with the HostedCluster and apply it. 

It is possible to create the cloud infrastructure portion separately so that the `hypershift create cluster powervs` command can just be used to create the HostedCluster spec and apply it.

Created HostedCluster can be destroyed and can be reinstalled with the same infra created earlier. 

In order to do this, you need to:
1. [Create PowerVS infrastructure](#creating-the-powervs-infra) 
2. [Create cluster](#creating-the-cluster)

## Creating the PowerVS infra

Please see [prerequisites](./prerequisites-and-env-guide.md/#prerequisites) before setting up the infra

Use the `hypershift create infra powervs` command:

    ./bin/hypershift create infra powervs --base-domain BASEDOMAIN
        --resource-group RESOURCE_GROUP \
        --infra-id INFRA_ID \
        --powervs-region POWERVS_REGION \
        --powervs-zone POWERVS_ZONE \
        --vpc-region VPC_REGION \
        --output-file OUTPUT_INFRA_FILE

E.g.:

      ./bin/hypershift create infra powervs --base-domain scnl-ibm.com \
      --resource-group hypershift-resource-group \
      --infra-id example \
      --powervs-region tok \
      --powervs-zone tok04 \
      --vpc-region jp-tok \
      --output-file infra.json

where

* BASEDOMAIN is the CIS base domain that will be used for your hosted cluster's ingress. It should be an existing CIS domain name.
* RESOURCE_GROUP is the resource group in IBMCloud where your infrastructure resources will be created.
* INFRA_ID is a unique name that will be used to name the infrastructure resources.
* POWERVS_REGION is the region where you want to create the powervs resources.
* POWERVS_ZONE is the zone under POWERVS_REGION where you want to create the powervs resources.
* VPC_REGION is the region where you want to create the vpc resources.
* OUTPUT_INFRA_FILE is the file where IDs of the infrastructure that has been created will be stored in JSON format.
  This file can then be used as input to the `hypershift create cluster powervs` command to populate
  the appropriate fields in the HostedCluster and NodePool resources.


Running this command should result in the following resources getting created:

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

      ./bin/hypershift create cluster powervs --base-domain scnl-ibm.com \
      --resource-group hypershift-resource-group \
      --infra-id example \
      --pull-secret ./pull-secret \
      --region tok --zone tok04 \
      --vpc-region jp-tok \
      --node-pool-replicas=2 \
      --infra-json infra.json