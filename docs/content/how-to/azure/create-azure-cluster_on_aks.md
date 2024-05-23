# Create an Azure Hosted Cluster on AKS
This document describes how to set up an Azure Hosted Cluster on an AKS management cluster with an ExternalDNS setup (starting in OCP 4.16).

If you already have an existing AKS cluster up and running, you can jump to the [Setup ExternalDNS](#setup-externaldns)

## Prerequisites
Obtain the az cli. See [this](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli) for instructions.

To create an AKS cluster, first set up your constants
```
RG="aks-test"
LOCATION="eastus"
AKS_CLUSTER_NAME="test"
```

Log in to your Azure account through the CLI
`az login`

Create an Azure Resource Group
```
az group create \
--name ${RG} \
--location ${LOCATION}
```

Create an AKS Cluster
```
az aks create \
--resource-group ${RG} \
--name ${AKS_CLUSTER_NAME} \
--node-count 3 \
--generate-ssh-keys \
--load-balancer-sku standard \
--os-sku AzureLinux \ 
--enable-fips-image
```

Finally, get your kubeconfig to your AKS cluster
```
az aks get-credentials \
--resource-group ${RG} \
--name ${AKS_CLUSTER_NAME} \
--overwrite-existing
```


## Setup ExternalDNS
First setup some constants. The resource group needs to be different from the resource group your AKS cluster is in.
```
RG="example-hosted-cluster"
LOCATION="eastus"
DNS_ZONE_NAME="azure.myDNSName.com"
EXTERNAL_DNS_NEW_SP_NAME="ExternalDnsServicePrincipal"
CLUSTER_NAME=example-cluster
AZURE_CREDS=/Users/user/.azure/credentials
AZURE_BASE_DOMAIN=my.base.domain.com
PULL_SECRET=/Users/user/all-the-pull-secrets.json
```

Create an Azure resource group and your DNS zone, which will be used with ExternalDNS
```
az group create --name ${RG} --location ${LOCATION}
az network dns zone create --resource-group ${RG} --name ${DNS_ZONE_NAME}
```

Create a service principal for the DNS zone
```
DNS_SP=$(az ad sp create-for-rbac --name ${EXTERNAL_DNS_NEW_SP_NAME})
EXTERNAL_DNS_SP_APP_ID=$(echo "$DNS_SP" | jq -r '.appId')
EXTERNAL_DNS_SP_PASSWORD=$(echo "$DNS_SP" | jq -r '.password')
```

Assign the rights for the service principal
```
DNS_ID=$(az network dns zone show --name ${DNS_ZONE_NAME} --resource-group ${RG} --query "id" --output tsv)
az role assignment create --role "Reader" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
az role assignment create --role "Contributor" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
```

Create a configuration file for our service principal. Change the user path to the azure.json below as desired.
```
cat <<-EOF > /Users/user/azure.json
{
"tenantId": "$(az account show --query tenantId -o tsv)",
"subscriptionId": "$(az account show --query id -o tsv)",
"resourceGroup": "$RG",
"aadClientId": "$EXTERNAL_DNS_SP_APP_ID",
"aadClientSecret": "$EXTERNAL_DNS_SP_PASSWORD"
}
EOF
```

Create an ExternalDNS needed secret with the azure.json configuration file
```
kubectl delete secret/azure-config-file --namespace "default"
kubectl create secret generic azure-config-file --namespace "default" --from-file /Users/user/azure.json
```

## Installing the HyperShift Operator
Make sure you add a record in your ExternalDNS provider from your @ records from your Azure DNS Zone.

Apply some CRDs that are missing
```
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
oc apply -f https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml
```

Install the HyperShift operator
```
hypershift install \
--enable-conversion-webhook=false \
--external-dns-provider=azure \
--external-dns-credentials /Users/user/azure.json \
--pull-secret ${PULL_SECRET} \
--external-dns-domain-filter ${DNS_ZONE_NAME} \
--managed-service ARO-HCP
```

## Create the Azure Hosted Cluster
Run the following command to create an Azure Hosted Cluster

```
hypershift create cluster azure \
--name $CLUSTER_NAME \
--azure-creds $AZURE_CREDS \
--location ${LOCATION} \
--node-pool-replicas 2 \
--base-domain $AZURE_BASE_DOMAIN \
--pull-secret $PULL_SECRET \
--generate-ssh \
--release-image quay.io/openshift-release-dev/ocp-release-nightly@sha256:b619707647800f7c382e7cb36e7b1026d82a576661274baffaf0585dd257fd1d \
--external-dns-domain ${DNS_ZONE_NAME} \
--resource-group-name ${RG} \
--annotations hypershift.openshift.io/pod-security-admission-label-override=baseline \
--annotations hypershift.openshift.io/certified-operators-catalog-image=registry.redhat.io/redhat/certified-operator-index@sha256:fc68a3445d274af8d3e7d27667ad3c1e085c228b46b7537beaad3d470257be3e \
--annotations hypershift.openshift.io/community-operators-catalog-image=registry.redhat.io/redhat/community-operator-index@sha256:4a2e1962688618b5d442342f3c7a65a18a2cb014c9e66bb3484c687cfb941b90 \
--annotations hypershift.openshift.io/redhat-marketplace-catalog-image=registry.redhat.io/redhat/redhat-marketplace-index@sha256:ed22b093d930cfbc52419d679114f86bd588263f8c4b3e6dfad86f7b8baf9844 \
--annotations hypershift.openshift.io/redhat-operators-catalog-image=registry.redhat.io/redhat/redhat-operator-index@sha256:59b14156a8af87c0c969037713fc49be7294401b10668583839ff2e9b49c18d6 \
--fips=true
```

## Deleting the Azure Hosted Cluster
You can delete the cluster by using the following command:
```
hypershift destroy cluster azure \
--name $CLUSTER_NAME \
--azure-creds $AZURE_CREDS \
--resource-group-name ${RG}
```
