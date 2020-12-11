## HyperShift

Guest clustering for [OpenShift](https://openshift.io).

### Prerequisites

* Admin access to an OpenShift cluster.
* The OpenShift `oc` CLI tool.
* [Kustomize](https://kustomize.io)

### Installation

Install HyperShift into the management cluster:

```bash
$ make install
```

Remove HyperShift from the management cluster:

```bash
$ make uninstall
```

### Create a cluster

First, create the following files containing secrets used by the example cluster:

- `config/example-cluster/pull-secret` a valid pull secret for image pulls.
- `config/example-cluster/ssh-key` an SSH public key for guest node access

Install the example cluster:

```bash
$ make install-example-cluster
```

If you want to see but not apply the example cluster resource (i.e. dry run), try:

```bash
$ make example-cluster
```

When the cluster is available, get the guest kubeconfig using:

```bash
$ oc get secret --namespace hypershift example-kubeconfig --template={{.data.value}} | base64 -D
```

To create additional node pools, create a resource like:

```yaml
apiVersion: hypershift.openshift.io/v1alpha1
kind: NodePool
metadata:
  namespace: hypershift
  name: example-extended
spec:
  clusterName: example
  autoScaling:
    max: 0
    min: 0
  nodeCount: 1
  platform:
    aws:
      instanceType: m5.large
```

And delete the cluster using:

```bash
$ oc delete --namespace hypershift openshiftclusters/example
```
