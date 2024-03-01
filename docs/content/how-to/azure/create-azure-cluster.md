# Create an Azure cluster
This document describes how to set up an Azure cluster with HyperShift.

## Prerequisites
To authenticate with Azure, an Application must be created through the web portal. Follow [this guide](https://docs.microsoft.com/en-us/azure/active-directory/develop/howto-create-service-principal-portal) to create one.

Afterward, create a credentials file that looks like this:

```
subscriptionId: <your_subscription_id>
tenantId: <your_tenant_id>
clientId: <your_client_id>
clientSecret: <your_client_secret>
```

## General
### Install the HyperShift Operator
No additional flags are need to install the HyperShift Operator in a nominal installation.

```
hypershift install 
```

### Creating the Cluster
After setting up the credentials file, creating a cluster is a simple matter of invoking the `hypershift` cli:

```
hypershift create cluster azure \
--pull-secret <pull_secret_file> \
--name <cluster_name> \
--azure-creds <path_to_azure_credentials_file> \
--location <location> \
--base-domain <base_domain> \
--release-image <release_image> \
--node-pool-replicas <number_of_replicas> \
```

## ExternalDNS
### Install the HyperShift Operator
This example uses optional ExternalDNS flags to set up external dns.

```
hypershift install \
--external-dns-provider=azure \
--external-dns-credentials <azure.json> \
--external-dns-domain-filter=<service_provider_domain>
```

See [external DNS docs](https://github.com/kubernetes-sigs/external-dns/blob/master/docs/tutorials/azure.md#creating-a-configuration-file-for-the-service-principal) for the format of the azure.json file.

### Creating the Cluster
After setting up the credentials file, creating a cluster is a simple matter of invoking the `hypershift` cli and setting the `external-dns-domain` flag:

```
hypershift create cluster azure \
--pull-secret <pull_secret_file> \
--name <cluster_name> \
--azure-creds <path_to_azure_credentials_file> \
--location <location> \
--base-domain <base_domain> \
--release-image <release_image> \
--node-pool-replicas <number_of_replicas \
--external-dns-domain=<service_provider_domain>
```
