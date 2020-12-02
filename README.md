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

Create a new guest cluster by creating an `OpenShiftCluster` resource. For now,
the cluster will be based on the version of the management cluster itself.

Here's an example:

```yaml
apiVersion: hypershift.openshift.io/v1alpha1
kind: OpenShiftCluster
metadata:
  name: guest-hello
spec:
  baseDomain: guest-hello.devcluster.openshift.com
  pullSecret: '{"auths": { ... }}'
  serviceCIDR: 172.31.0.0/16
  podCIDR: 10.132.0.0/14
  sshKey: 'ssh-rsa ...'
  initialComputeReplicas: 1
```

Get the guest cluster's kubeconfig using:

```bash
$ oc get secret --namespace guest-hello admin-kubeconfig --template={{.data.kubeconfig}} | base64 -D
```

You can create additional nodePools:

```yaml
apiVersion: hypershift.openshift.io/v1alpha1
kind: NodePool
metadata:
  name: guest-hello-custom-nodepool
  namespace: hypershift
spec:
  clusterName: guest-hello
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
$ oc delete --namespace hypershift openshiftclusters/guest-hello
```
