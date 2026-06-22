# Creating a HyperShift CI cluster

## Prerequisites

- OpenShift CLI (oc)
- An OCP cluster ([ROSA instructions](https://www.rosaworkshop.io/rosa/2-deploy/#automatic-mode))

## Install

Deploy the hypershift-ci-1 manifests:

```shell
oc apply -f hypershift-ci-1.yaml
```

After initial installation or as part of a credentials rotation, create a
kubeconfig from the admin SA token which can be injected into CI jobs:

```shell
oc serviceaccounts --namespace hypershift-ops create-kubeconfig admin > /tmp/hypershift-ci-1.kubeconfig
```

Store the kubeconfig in Vault [under the clusters directory](https://vault.ci.openshift.org/ui/vault/secrets/kv/list/selfservice/hypershift-team/ops/clusters/) in a secret named `hypershift-ci-1` with the following schema:

```json
{
  "hypershift-ops-admin.kubeconfig": "<kubeconfig contents>",
  "secretsync/target-name": "hypershift-ci-1",
  "secretsync/target-namespace": "ci,test-credentials"
}
```

## Template for developer namespaces

Deploy the template for developer namespaces (only needs to be done one time per CI cluster):

```shell
oc apply -f dev-namespace-template.yaml
```


## Using the developer namespace template

This will enable developers to create their own namespace (and kubeconfig) for their cluster using the following command:

```shell
NAME=your-name
oc new-app developer-namespace -p NAME=$NAME
token=$(oc get secrets -n $NAME -o name | grep $NAME-dev-token | xargs oc get -o jsonpath='{.data.token}' -n $NAME | base64 -d)
oc login --token=$token
```

After doing this, the default context on your kubeconfig will be the low-privilege service account in your namespace, useful
for creating HostedClusters and NodePools. If you still need privileged access, you can pass `--context=admin` to any `oc` command.
