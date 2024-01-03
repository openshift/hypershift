# Create an Azure cluster
This document describes how to set up an Azure cluster with Hypershift.

## Prerequisites
In order to authenticate with Azure, an Application must be created through the web portal. Follow [this guide](https://docs.microsoft.com/en-us/azure/active-directory/develop/howto-create-service-principal-portal) to create one.

Afterward, create a credentials file that looks like this:

```
subscriptionId: <your_subscription_id>
tenantId: <your_tenant_id>
clientId: <your_client_id>
clientSecret: <your_client_secret>
```

## Install the Hypershift Operator
This example uses optional external dns flags to set up external dns.

```
hypershift install --external-dns-provider=azure \
--external-dns-credentials <azure.json> \
--external-dns-domain-filter=<service_provider_domain>
```

See [external DNS docs](https://github.com/kubernetes-sigs/external-dns/blob/master/docs/tutorials/azure.md#creating-a-configuration-file-for-the-service-principal) for the format of the azure.json file.

## Creating the Cluster
After the credentials file was set up, creating a cluster is a simple matter of invoking the `hypershift` cli:

```
hypershift create cluster azure --pull-secret <pull_secret_file> \
--name <cluster_name> \
--azure-creds <path_to_azure_credentials_file> \
--location eastus --base-domain <base_domain> \
--release-image <release_image> \
--node-pool-replicas 3 \
--external-dns-domain=<service_provider_domain>
```
