#!/bin/bash
set +x

# Prerequisites: define these constants
LOCATION="eastus"
KV_RG_NAME="example-kms"
KV_NAME="example-key-vault"
KEY_NAME="example-key"
SUBSCRIPTION_ID="<your-subscription-id-here>"

# This is the object ID of the KMS Managed Identity. This object ID can be found under the enterprise application for
# your KMS Managed Identity.
USER_OBJECT_ID="<fill-me-out>"

# This is the object ID of the KMS Managed Identity. This object ID can be found under the enterprise application for
# your KMS Managed Identity.
OBJECT_ID="<fill-me-out>"

# Create a resource group to hold the Key Vault
az group create --name $KV_RG_NAME --location $LOCATION

# Create the Key Vault
az keyvault create --name $KV_NAME --resource-group $KV_RG_NAME --location $LOCATION --enable-rbac-authorization

## Associate your SP/Account with the Key Vault; this is so you can use your SP/Account can use the CLI to create the key in the key vault
az role assignment create \
    --assignee ${USER_OBJECT_ID} \
    --scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/$KV_RG_NAME/providers/Microsoft.KeyVault/vaults/${KV_NAME} \
    --role "Key Vault Administrator"

#Create a key in the Key Vault
export KEY_ID=$(az keyvault key create --vault-name $KV_NAME --name $KEY_NAME --protection software --kty RSA --query key.kid -o tsv)

# Assign the Key Vault Crypto User role to the KMS Managed Identity
az role assignment create --assignee $OBJECT_ID --role "Key Vault Crypto User" --scope "$(az keyvault show --name $KV_NAME --query "resourceGroup" -o tsv | xargs -I{} az group show --name {} --query "id" -o tsv)"
