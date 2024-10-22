#!/bin/bash
set -x

# Constants
MGMT_DNS_ZONE_NAME="azure.blah.com"
PULL_SECRET="/Users/your-username/all-the-pull-secrets.json"
SERVICE_PRINCIPAL_FILEPATH="/Users/your-username/azure_mgmt.json"
TAG="quay.io/user/hypershift:mytag1"

######################################## HyperShift Operator Install ########################################

# Apply some CRDs that are missing
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
oc apply -f https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml

# Install HO; leave off --hypershift-image line if you aren't testing with a particular HO image
# tech-preview-no-upgrade and aro-hcp-key-vault-users-client-id is needed for managed identity
${HYPERSHIFT_BINARY_PATH}/hypershift install \
--enable-conversion-webhook=false \
--external-dns-provider=azure \
--external-dns-credentials ${SERVICE_PRINCIPAL_FILEPATH} \
--pull-secret ${PULL_SECRET} \
--external-dns-domain-filter ${MGMT_DNS_ZONE_NAME} \
--managed-service ARO-HCP \
--aro-hcp-key-vault-users-client-id "${AZURE_KEY_VAULT_AUTHORIZED_USER_ID}" \
--hypershift-image quay.io/username/hypershift:${TAG} \
--tech-preview-no-upgrade

set +x