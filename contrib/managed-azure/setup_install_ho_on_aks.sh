#!/bin/bash
set -x

# Constants
DNS_ZONE_NAME="azure.blah.com"
HYPERSHIFT_BINARY_PATH="/User/hypershift/bin"
PULL_SECRET="/Users/your-username/all-the-pull-secrets.json"
SERVICE_PRINCIPAL_FILEPATH="/Users/your-username/azure_mgmt.json"
TAG="quay.io/user/hypershift:mytag1"

######################################## HyperShift Operator Install ########################################

# Apply some CRDs that are missing
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
oc apply -f https://raw.githubusercontent.com/openshift/api/6bababe9164ea6c78274fd79c94a3f951f8d5ab2/route/v1/zz_generated.crd-manifests/routes.crd.yaml

# Install HO; leave off --hypershift-image line if you aren't testing with a particular HO image
${HYPERSHIFT_BINARY_PATH}/hypershift install \
--enable-conversion-webhook=false \
--external-dns-provider=azure \
--external-dns-credentials ${SERVICE_PRINCIPAL_FILEPATH} \
--pull-secret ${PULL_SECRET} \
--external-dns-domain-filter ${DNS_ZONE_NAME} \
--managed-service ARO-HCP \
--aro-hcp-key-vault-users-client-id "${AZURE_KEY_VAULT_AUTHORIZED_USER_ID}" \
--limit-crd-install Azure \
--hypershift-image quay.io/user/hypershift:${TAG}

set +x