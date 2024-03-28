#!/bin/bash
set -x

# Constants
LOCATION="eastus"
RG="hc-test"
DNS_ZONE_NAME="azure.blah.com"
EXTERNAL_DNS_NEW_SP_NAME="ExternalDnsServicePrincipal"
CLUSTER_NAME=<cluster_name>
AZURE_CREDS=<path_to>/credentials
AZURE_BASE_DOMAIN=<domain>
PULL_SECRET=<path_to>/all-the-pull-secrets.json
PATH_TO_HYPERSHIFT_BIN=<path>
RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release-nightly@sha256:b619707647800f7c382e7cb36e7b1026d82a576661274baffaf0585dd257fd1d
PATH_TO_AZURE_JSON=<path>

######################################## ExternalDNS Setup ########################################
# Clear out existing Azure RG
az group delete -n ${RG} --yes

# Create Azure RG and DNS Zone
az group create --name ${RG} --location ${LOCATION}
az network dns zone create --resource-group ${RG} --name ${DNS_ZONE_NAME}

# Creating a service principal
DNS_SP=$(az ad sp create-for-rbac --name ${EXTERNAL_DNS_NEW_SP_NAME})
EXTERNAL_DNS_SP_APP_ID=$(echo "$DNS_SP" | jq -r '.appId')
EXTERNAL_DNS_SP_PASSWORD=$(echo "$DNS_SP" | jq -r '.password')

# Assign the rights for the service principal
DNS_ID=$(az network dns zone show --name ${DNS_ZONE_NAME} --resource-group ${RG} --query "id" --output tsv)
az role assignment create --role "Reader" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
az role assignment create --role "Contributor" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"

# Creating a configuration file for our service principal
cat <<-EOF > ${PATH_TO_AZURE_JSON}/azure.json
{
  "tenantId": "$(az account show --query tenantId -o tsv)",
  "subscriptionId": "$(az account show --query id -o tsv)",
  "resourceGroup": "$RG",
  "aadClientId": "$EXTERNAL_DNS_SP_APP_ID",
  "aadClientSecret": "$EXTERNAL_DNS_SP_PASSWORD"
}
EOF

# Create needed secret with azure.json
kubectl delete secret/azure-config-file --namespace "default"
kubectl create secret generic azure-config-file --namespace "default" --from-file ${PATH_TO_AZURE_JSON}/azure.json

######################################## HyperShift Operator Install ########################################

# Apply some CRDs that are missing
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
oc apply -f https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml

# Install HO
# 2024-03-01 it will fail if you have the conversion webhook enabled
${PATH_TO_HYPERSHIFT_BIN}/hypershift install \
--enable-conversion-webhook=false \
--external-dns-provider=azure \
--external-dns-credentials ${PATH_TO_AZURE_JSON}/azure.json \
--pull-secret ${PULL_SECRET} \
--external-dns-domain-filter ${DNS_ZONE_NAME} \
--managed-service ARO-HCP

######################################## Create Hosted Cluster ########################################

${PATH_TO_HYPERSHIFT_BIN}/hypershift create cluster azure \
--name $CLUSTER_NAME \
--azure-creds $AZURE_CREDS \
--location ${LOCATION} \
--node-pool-replicas 2 \
--base-domain $AZURE_BASE_DOMAIN \
--pull-secret $PULL_SECRET \
--generate-ssh \
--release-image ${RELEASE_IMAGE} \
--external-dns-domain ${DNS_ZONE_NAME} \
--resource-group-name ${RG} \
--annotations hypershift.openshift.io/pod-security-admission-label-override=baseline \
--annotations hypershift.openshift.io/certified-operators-catalog-image=registry.redhat.io/redhat/certified-operator-index@sha256:fc68a3445d274af8d3e7d27667ad3c1e085c228b46b7537beaad3d470257be3e \
--annotations hypershift.openshift.io/community-operators-catalog-image=registry.redhat.io/redhat/community-operator-index@sha256:4a2e1962688618b5d442342f3c7a65a18a2cb014c9e66bb3484c687cfb941b90 \
--annotations hypershift.openshift.io/redhat-marketplace-catalog-image=registry.redhat.io/redhat/redhat-marketplace-index@sha256:ed22b093d930cfbc52419d679114f86bd588263f8c4b3e6dfad86f7b8baf9844 \
--annotations hypershift.openshift.io/redhat-operators-catalog-image=registry.redhat.io/redhat/redhat-operator-index@sha256:59b14156a8af87c0c969037713fc49be7294401b10668583839ff2e9b49c18d6

set +x