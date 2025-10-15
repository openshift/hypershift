
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# User variables.
if [[ ! -f "./user-vars.sh" ]]; then
  echo "Error: ./user-vars.sh not found. Please create it before running this script."
  exit 1
fi
source "./user-vars.sh"

# Internal Variables shared by the scripts.
# We export the values here so any script can be commented out from setup_all.sh without breaking the other scripts.
export PERSISTENT_RG_NAME="os4-common"
export PERSISTENT_RG_LOCATION="centralus"
# Tip - the value of `$PARENT_DNS_ZONE` may be different for different teams.
# Check `os4-common` resource group associated with your subscription-id for pre-existing DNS zones.
# If there are multiple DNS zones more than likely the parent DNS zone is the common suffix among them.
export PARENT_DNS_ZONE="hypershift.azure.devcluster.openshift.com"
export MGMT_DNS_ZONE_NAME="${PREFIX}.${PARENT_DNS_ZONE}"

ACCOUNT_DETAILS=$(az account show --query '{subscriptionId: id, tenantId: tenantId}' -o json)
export SUBSCRIPTION_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.subscriptionId')
export TENANT_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.tenantId')

export EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH="./external_DNS_SA.json"
export AKS_RG=${PREFIX}"-aks-rg"
export AKS_CLUSTER_NAME=${PREFIX}"-aks-cluster"

export OIDC_ISSUER_URL="https://${OIDC_ISSUER_NAME}.blob.core.windows.net/${OIDC_ISSUER_NAME}"
export DP_OUTPUT_FILE="./dp-output.json"
export CP_OUTPUT_FILE="./cp-output.json"

export SA_TOKEN_ISSUER_PUBLIC_KEY_PATH="./serviceaccount-signer.public"
export SA_TOKEN_ISSUER_PRIVATE_KEY_PATH="./serviceaccount-signer.private"
