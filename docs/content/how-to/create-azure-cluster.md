 # Creating an Azure cluster

This document describes how to set up an Azure cluster with Hypershift.

## Prerequisites

In order to authenticate with Azure, an Application must be created through the web portal. Follow [this guide](https://docs.microsoft.com/en-us/azure/active-directory/develop/howto-create-service-principal-portal) to create one.

Afterwards, create a credentials file that looks like this:

```
AZURE_SUBSCRIPTION_ID: <your_subscription_id>
AZURE_TENANT_ID: <your_tenant_id>
AZURE_CLIENT_ID: <your_client_id>
AZURE_CLIENT_SECRET: <your_client_secret>
```

## Creating the cluster

After the credentials file was set up, creating a cluster is a simple matter of invoking the `hypershift` cli:


```
hypershift create cluster azure --pull-secret <pull_secret_file> --name <cluster_name> --azure-creds <path_to_azure_credentials_file> --location eastus
```
