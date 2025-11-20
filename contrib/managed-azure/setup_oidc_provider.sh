#!/bin/bash
set -o nounset
set -x

# See https://github.com/nutanix-cloud-native/openshift-cloud-credential-operator/blob/master/docs/ccoctl.md#creating-rsa-keys-1
# for more info and how to build the ccoctl binary.

# Prerequisites.
OIDC_ISSUER_NAME=${OIDC_ISSUER_NAME:?"Provide OIDC issuer name"}
PERSISTENT_RG_LOCATION=${PERSISTENT_RG_LOCATION:?"Provide persistent resource group location"}
PERSISTENT_RG_NAME=${PERSISTENT_RG_NAME:?"Provide persistent resource group name"}
TENANT_ID=${TENANT_ID:?"Provide tenant ID"}
SUBSCRIPTION_ID=${SUBSCRIPTION_ID:?"Provide subscription ID"}
SA_TOKEN_ISSUER_PUBLIC_KEY_PATH=${SA_TOKEN_ISSUER_PUBLIC_KEY_PATH:?"Provide service account token issuer public key path"}

ccoctl azure create-key-pair

ccoctl azure create-oidc-issuer \
--oidc-resource-group-name $PERSISTENT_RG_NAME \
--tenant-id $TENANT_ID \
--region $PERSISTENT_RG_LOCATION \
--name $OIDC_ISSUER_NAME \
--subscription-id $SUBSCRIPTION_ID \
--public-key-file $SA_TOKEN_ISSUER_PUBLIC_KEY_PATH

set +x
