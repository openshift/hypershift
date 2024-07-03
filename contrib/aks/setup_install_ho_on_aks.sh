#!/bin/bash
set -x

# Constants
MGMT_DNS_ZONE_NAME="azure.blah.com"
HYPERSHIFT_BINARY_PATH=<path>
CUSTOM_IMAGE="quay.io/user/hypershift:mytag1"

######################################## HyperShift Operator Install ########################################

# Apply some CRDs that are missing
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
oc apply -f https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml

# Install HyperShift Operator (HO)
# Include this flag in the command below if you want to run a custom HO image
# --hypershift-image ${CUSTOM_IMAGE}

${HYPERSHIFT_BINARY_PATH}/hypershift install \
--enable-conversion-webhook=false \
--external-dns-provider=azure \
--external-dns-credentials /Users/user/azure_mgmt.json \
--pull-secret /Users/user/all-the-pull-secrets.json \
--external-dns-domain-filter ${MGMT_DNS_ZONE_NAME} \
--managed-service ARO-HCP

set +x