#!/bin/bash
set -x

# See https://github.com/nutanix-cloud-native/openshift-cloud-credential-operator/blob/master/docs/ccoctl.md#creating-rsa-keys-1
# for more info and how to build the ccoctl binary.

# Prerequisites.
OIDC_ISSUER_NAME="${OIDC_ISSUER_NAME:-}"
PERSISTENT_RG_LOCATION="${PERSISTENT_RG_LOCATION:-}"

# Local.
SA_TOKEN_ISSUER_PUBLIC_KEY_PATH=${SA_TOKEN_ISSUER_PUBLIC_KEY_PATH:-}
SA_TOKEN_ISSUER_PRIVATE_KEY_PATH=${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH:-}

ccoctl azure create-key-pair

ccoctl azure create-oidc-issuer \
--oidc-resource-group-name $PERSISTENT_RG_NAME \
--tenant-id $TENANT_ID \
--region ${PERSISTENT_RG_LOCATION} \
--name $OIDC_ISSUER_NAME \
--subscription-id $SUBSCRIPTION_ID \
--public-key-file $SA_TOKEN_ISSUER_PUBLIC_KEY_PATH

set +x
