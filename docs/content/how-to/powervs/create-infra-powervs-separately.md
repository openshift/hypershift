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

### Setting up IBM Cloud Authentication
Authenticate IBM Cloud Clients by setting the `IBMCLOUD_API_KEY` environment var to your API Key.

### Setting custom endpoints for IBM Cloud services
Set the following environment var to set the custom endpoint.
```
IBMCLOUD_POWER_API_ENDPOINT - to setup PowerVS custom endpoint
IBMCLOUD_VPC_API_ENDPOINT - to setup VPC custom endpoint
IBMCLOUD_PLATFORM_API_ENDPOINT - to setup platform services custom endpoint
```

## Creating the PowerVS infra

Set up the [authentication](#setting-up-ibm-cloud-authentication) by following this section

Use the `hypershift create infra powervs` command:

    ./bin/hypershift create infra powervs --base-domain BASEDOMAIN
        --resource-group RESOURCE_GROUP \
        --infra-id INFRA_ID \
        --powervs-region POWERVS_REGION \
        --powervs-zone POWERVS_ZONE \
        --vpc-region VPC_REGION \
        --output-file OUTPUT_INFRA_FILE

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

* 1 VPC with Subnet
* 1 PowerVS Cloud Instance
* 1 DHCP Service
  * 1 DHCP Private Network
  * 1 DHCP Public Network
* 1 Cloud Connection

## Creating the Cluster

TODO