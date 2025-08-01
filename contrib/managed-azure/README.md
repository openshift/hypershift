# General
This directory contains several developer-focused scripts and instructions related to setting up an AKS management cluster and create a HostedCluster replicating what we do in ARO environments.

If you do have issues with these scripts or need further help. Please reach out to #project-hypershift on Red Hat Slack.

Steps:

Create a ServicePrincipal to let hypershift cli create the HostedCluster infra for you.

```
SP_DETAILS=$(az ad sp create-for-rbac --name "${PREFIX}-sp" --role Contributor --scopes "/subscriptions/$SUBSCRIPTION_ID" -o json)
CLIENT_ID=$(echo "$SP_DETAILS" | jq -r '.appId')
CLIENT_SECRET=$(echo "$SP_DETAILS" | jq -r '.password')

cat <<EOF > ./azure-creds.json
{
   "subscriptionId": "$SUBSCRIPTION_ID",
   "tenantId": "$TENANT_ID",
   "clientId": "$CLIENT_ID",
   "clientSecret": "$CLIENT_SECRET"
}
# EOF
```

!!! warning

    In order for your Hypershift cluster to be created properly, the Microsoft Graph `Application.ReadWrite.OwnedBy`
    permission must be added to your Service Principal and it also must be assigned to User Access Administrator at the
    subscription level.

    In most cases, you'll need to submit a DPTP request to have this done.

From this repo root folder:

```
mkdir dev
cd dev
```

Create your user-vars.sh file. E.g.

```
cat <<EOF > user-vars.sh
# User variables.
export PREFIX="USER-management"
export PULL_SECRET="/path/pull-secret.txt"
export HYPERSHIFT_BINARY_PATH="/path/go/src/github.com/openshift/hypershift/bin/"
export HYPERSHIFT_IMAGE="quay.io/hypershift/hypershift-operator:latest"
export RELEASE_IMAGE="quay.io/openshift-release-dev/ocp-release:4.20.0-ec.3-multi"
export LOCATION="eastus"
export AZURE_CREDS="/path/azure-hypershift-dev.json"
# Azure storage account names must be between 3 and 24 characters in length and may contain numbers and lowercase letters only.
export OIDC_ISSUER_NAME="USERmanagement"
EOF
```

Run it

```
../contrib/managed-azure/setup_all.sh
```

### First-time Setup

When setting up your first cluster, use the `--first-time` flag to create the one-time setup resources that should be reused across multiple clusters to avoid Azure quota issues:

```
../contrib/managed-azure/setup_all.sh --first-time
```

The `--first-time` flag automatically handles the creation of:
- Service principals and Key Vault ([setup_MIv3_kv.sh](./setup_MIv3_kv.sh))
- OIDC issuer ([setup_oidc_provider.sh](./setup_oidc_provider.sh))
- Data plane identities ([setup_dataplane_identities.sh](./setup_dataplane_identities.sh))

For subsequent clusters, run the script without this flag to skip the one-time setup and reuse existing resources.

You can comment out steps from setup_all.sh to run only what you want.

## Microsoft Velero Extension

We already include the managed identity/service principal related with Velero, so you just need to execute these commands:

```
../contrib/managed-azure/setup_backup_extension.sh --setup-backup-requirements --deploy-backup-extension
```

or in separate steps:

```
../contrib/managed-azure/setup_backup_extension.sh --setup-backup-requirements
../contrib/managed-azure/setup_backup_extension.sh --deploy-backup-extension
```