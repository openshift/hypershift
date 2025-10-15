#!/bin/bash
set -x

# Prerequisites.
HYPERSHIFT_BINARY_PATH="${HYPERSHIFT_BINARY_PATH:-}"
MGMT_DNS_ZONE_NAME="${MGMT_DNS_ZONE_NAME:-}"
PULL_SECRET="${PULL_SECRET:-}"
EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH="${EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH:-}"
HYPERSHIFT_IMAGE="${HYPERSHIFT_IMAGE:-}"
AKS_RG="${AKS_RG:-}"
AKS_CLUSTER_NAME="${AKS_CLUSTER_NAME:-}"
AZURE_KEY_VAULT_AUTHORIZED_USER_ID=$(az aks show -n ${AKS_CLUSTER_NAME} -g ${AKS_RG} | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.clientId -r)

# Apply some CRDs that are missing
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
oc apply -f https://raw.githubusercontent.com/openshift/api/6bababe9164ea6c78274fd79c94a3f951f8d5ab2/route/v1/zz_generated.crd-manifests/routes.crd.yaml

# Install HO; leave off --hypershift-image line if you aren't testing with a particular HO image
# tech-preview-no-upgrade and aro-hcp-key-vault-users-client-id is needed for managed identity
${HYPERSHIFT_BINARY_PATH}/hypershift install \
--enable-conversion-webhook=false \
--external-dns-provider=azure \
--external-dns-credentials ${EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH} \
--pull-secret ${PULL_SECRET} \
--external-dns-domain-filter ${MGMT_DNS_ZONE_NAME} \
--managed-service ARO-HCP \
--aro-hcp-key-vault-users-client-id "${AZURE_KEY_VAULT_AUTHORIZED_USER_ID}" \
--hypershift-image ${HYPERSHIFT_IMAGE} \
--limit-crd-install Azure \
--tech-preview-no-upgrade

set +x
