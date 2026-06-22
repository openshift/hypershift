# Preparing Root CI Azure Management Cluster from AWS Cluster

## Prerequisites
- OpenShift CLI (oc)
- OCP cluster (ROSA)
- Hypershift CLI (hypershift)


## Step 1: Project Initialization

Create a new project named azure in the OCP cluster, using the following command:

```shell
oc new project azure
```

## Step 2: Create the Azure Management Cluster

ARO creds can be found [here](https://vault.ci.openshift.org/ui/vault/secrets/kv/show/selfservice/hypershift-team/ops/hypershift-ci-jobs-azurecreds) or this [guide](https://learn.microsoft.com/en-us/entra/identity-platform/howto-create-service-principal-portal) can be followed to generate new ones.

**Note**: To preserve the mgmt cluster follow this [guide](https://source.redhat.com/groups/public/openshift/openshift_wiki/openshift_dev_microsoft_azure__azure_government#jive_content_id_Resource_pruning) 

```shell
hypershift create cluster azure \
--namespace azure \
--name azure-mgmt \
--pull-secret <PATH_TO_PULLSECRET> \
--azure-creds <ARO_CREDS_FROM_VAULT> \
--location eastus \
--release-image quay.io/openshift-release-dev/ocp-release:4.15.0-rc.0-x86_64 \
--base-domain hypershift.azure.devcluster.openshift.com \
--node-pool-replicas 3
```

## Step 3: GitHub IDP for Azure Management Cluster

Register a GitHub application using this [guide](https://docs.openshift.com/container-platform/4.8/authentication/identity_providers/configuring-github-identity-provider.html#identity-provider-registering-github_configuring-github-identity-provider)
- Use console URL for homepage
- Use OauthCallBackURLTemplate from HC.Status for callback URL

Create a secret in the azure namespace with the client secret from the GitHub application

```shell
oc create secret generic github-idp --from-literal=clientSecret=<CLIENT_SECRET> -n azure-mgmt
```

Add GitHub oauth to HC.Spec.Configuration

```shell
oauth:
  identityProviders:
    - github:
        clientID: *******
        clientSecret:
          name: github-idp
        organizations:
          - openshift-sjenning-dev
      mappingMethod: claim
      name: github
      type: GitHub
```

## Step 4: Create kubeconfig for Azure management cluster

```shell
hypershift create kubeconfig --name azure-mgmt --namespace azure > /tmp/hypershift-azure-mgmt.kubeconfig
```


## Step 5: Install Hypershift on Azure management cluster

Login to the Azure management cluster using the kubeconfig created in step 4

```shell
oc apply -f hypershift-install-azure.yaml
```

## Step 6: Save CI kubeconfig in vault

```shell
cat <<EOF > /tmp/azure-mgmt.kubeconfig
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: <CERTIFICATE_AUTHORITY_DATA>
    server: <CLUSTER_SERVER_URL>
  name: <CLUSTER_SERVER_URL>
contexts:
- context:
    cluster: <CLUSTER_SERVER_URL> 
    namespace: hypershift-ops
    user: admin
  name: admin
current-context: admin
kind: Config
preferences: {}
users:
- name: admin
  user:
    token: <TOKEN>
```

**Note**: Token and certificate-authority-data can be found in admin secret under hypershift-ops namespace.

Store the kubeconfig in Vault [under the clusters directory](https://vault.ci.openshift.org/ui/vault/secrets/kv/list/selfservice/hypershift-team/ops/clusters/) in a secret named `azure-hypershift-ci` with the following schema:

```json
{
  "hypershift-azure-admin.kubeconfig": "<kubeconfig contents>",
  "secretsync/target-name": "azure-hypershift-ci",
  "secretsync/target-namespace": "ci,test-credentials"
}
```

## Step 6: Configure permissions for users

Edit the subjects in cluster-admin ClusterRoleBinding to add users:

```shell
oc edit clusterrolebinding/cluster-admin
```