# Azure CCM managed identity
Here's an example of how to create an MSI (for Azure CCM in this case) and how to include it in the HyperShift CLI

## Create the managed identity
`az identity create --name "${AZURE_CCM_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}"`
## Get the managed identity principal ID
`AZURE_CCM_MSI_ID=$(az identity show --name "${AZURE_CCM_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}" --query principalId --output tsv)`
## Gets resource id
`AZURE_CCM_MSI_RESOURCE_ID=$(az identity show --name "${AZURE_CCM_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}" --query id --output tsv)`

## Get the VMSS name from my AKS cluster; we need this until the sidecar is implemented
`VMSS_NAME=$(az vmss list --resource-group MC_brcox-aks-rg_brcox-aks-cluster_eastus | jq '.[] | .name' | sed 's/"//g')`

## Assign the MSI to the VMSS
`az vmss identity assign -g MC_brcox-aks-rg_brcox-aks-cluster_eastus --name "${VMSS_NAME}" --identities "${AZURE_CCM_MSI_RESOURCE_ID}"`

## Contributor over the managed RG for the LB
```
az role assignment create \
--assignee-object-id "${AZURE_CCM_MSI_ID}" \
--role "Contributor" \
--scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${MANAGED_RG_NAME}" \
--assignee-principal-type ServicePrincipal
```

## Network contributor over the NSG RG
```
az role assignment create \
--assignee-object-id "${AZURE_CCM_MSI_ID}" \
--role "Network Contributor" \
--scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${CUSTOMER_NSG_RG_NAME}" \
--assignee-principal-type ServicePrincipal
```

## Get the client id of the managed identity
`AZURE_CCM_MSI_CLIENT_ID=$(az identity show --name "${AZURE_CCM_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}" --query clientId --output tsv)`


 ## Including them in the HyperShift CLI
 ```
 ${HYPERSHIFT_BINARY_PATH}/hypershift create cluster azure \
 --name "$CLUSTER_NAME" \
 --azure-creds $AZURE_CREDS \
 --location ${LOCATION} \
 --node-pool-replicas 2 \
 --base-domain $AZURE_BASE_DOMAIN \
 --pull-secret $PULL_SECRET \
 --generate-ssh \
 --release-image ${RELEASE_IMAGE} \
 --external-dns-domain ${MGMT_DNS_ZONE_NAME} \
 --resource-group-name "${MANAGED_RG_NAME}" \
 --vnet-id "${GetVnetID}" \
 --subnet-id "${GetSubnetID}" \
 --network-security-group-id "${GetNsgID}" \
 --azure-ccm-msi-client-id "${AZURE_CCM_MSI_CLIENT_ID}" \
 --annotations hypershift.openshift.io/pod-security-admission-label-override=baseline \
 --control-plane-operator-image=quay.io/rh_ee_brcox/hypershift:"${TAG}" \
 --annotations hypershift.openshift.io/certified-operators-catalog-image=registry.redhat.io/redhat/certified-operator-index@sha256:fc68a3445d274af8d3e7d27667ad3c1e085c228b46b7537beaad3d470257be3e \
 --annotations hypershift.openshift.io/community-operators-catalog-image=registry.redhat.io/redhat/community-operator-index@sha256:4a2e1962688618b5d442342f3c7a65a18a2cb014c9e66bb3484c687cfb941b90 \
 --annotations hypershift.openshift.io/redhat-marketplace-catalog-image=registry.redhat.io/redhat/redhat-marketplace-index@sha256:ed22b093d930cfbc52419d679114f86bd588263f8c4b3e6dfad86f7b8baf9844 \
 --annotations hypershift.openshift.io/redhat-operators-catalog-image=registry.redhat.io/redhat/redhat-operator-index@sha256:59b14156a8af87c0c969037713fc49be7294401b10668583839ff2e9b49c18d6 \
 --fips=true \
 --marketplace-publisher azureopenshift \
 --marketplace-offer aro4 \
 --marketplace-sku aro_417 \
 --marketplace-version 417.94.20240701
 ```