## HyperShift

Guest clustering for [OpenShift](https://openshift.io).

### Prerequisites

* Admin access to an OpenShift cluster (version 4.7).
* The OpenShift `oc` CLI tool.
* The `hypershift` CLI tool:

        $ make hypershift

### Install HyperShift

Install HyperShift into the management cluster:

```bash
$ bin/hypershift install
```

Remove HyperShift from the management cluster:

```bash
$ bin/hypershift install --render | oc delete -f -
```

### Create an example cluster

Prerequisites:

- A valid pull secret file for image pulls.
- An SSH public key file for guest node access.
- An [aws credentials file](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html).

Install the example cluster:

```bash
$ bin/hypershift create cluster \
    --pull-secret /my/pull-secret \
    --aws-creds /my/aws-credentials \
    --ssh-key /my/ssh-public-key
```

When the cluster is available, get the guest kubeconfig using:

```bash
$ oc get secret --namespace example admin-kubeconfig --template={{.data.value}} | base64 -D
```

To create additional node pools, create a resource like:

```yaml
apiVersion: hypershift.openshift.io/v1alpha1
kind: NodePool
metadata:
  namespace: clusters
  name: example-extended
spec:
  clusterName: example
  nodeCount: 1
  platform:
    aws:
      instanceType: m5.large
```

with autoscaling:

```yaml
apiVersion: hypershift.openshift.io/v1alpha1
kind: NodePool
metadata:
  namespace: clusters
  name: example-extended
spec:
  clusterName: example
  autoScaling:
    max: 5
    min: 1
  platform:
    aws:
      instanceType: m5.large
```

And delete the cluster using:

```bash
$ oc delete --namespace clusters
```
