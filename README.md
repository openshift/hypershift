# HyperShift

HyperShift enables [OpenShift](https://openshift.io/) administrators to offer managed OpenShift control planes as a service.

## How to install the HyperShift CLI

The `hypershift` CLI tool helps you install and work with HyperShift.

**Prerequisites:**

* Go 1.16

Install the `hypershift` CLI using Go:

```shell
go install github.com/openshift/hypershift@latest
```

The `hypershift` tool will be installed to `$GOBIN/hypershift`.


## How to install HyperShift

HyperShift is deployed into an existing OpenShift cluster which will host the managed control planes.

**Prerequisites:**

* Admin access to an OpenShift cluster (version 4.7+) specified by the `KUBECONFIG` environment variable
* The OpenShift `oc` CLI tool
* The `hypershift` CLI tool

Install HyperShift into the management cluster:

```shell
hypershift install
```

To uninstall HyperShift, run:

```shell
hypershift install --render | oc delete -f -
```

## How to create a hosted cluster

The `hypershift` CLI tool comes with commands to help create an example hosted cluster. The cluster will come with a node pool consisting of two workers nodes.

**Prerequisites:**

- An OpenShift cluster with HyperShift installed
- Admin access to the OpenShift cluster specified by the `KUBECONFIG` environment variable
- The `hypershift` CLI tool
- The OpenShift `oc` CLI tool.
- A valid pull secret file for the `quay.io/openshift-release-dev` repository
- An [AWS credentials file](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html) with permissions to create infrastructure for the cluster

Run the `hypershift` command to create an IAM instance profile for your workers:
```shell
hypershift create iam aws --aws-creds ~/.aws/credentials
```
NOTE: The default profile name is `hypershift-worker-profile`. To use a different name 
(for example, in a shared account), use the `--profile-name` flag. The worker instance
profile only needs to be created once per account and you can reuse it as needed
for your clusters.

Run the `hypershift` command to create a cluster named `example` in the `clusters`
namespace, including the cloud infrastructure to support it.

```shell
hypershift create cluster \
  --pull-secret /my/pull-secret \
  --aws-creds ~/.aws/credentials
```

Eventually the cluster's kubeconfig will become available and can be printed to
standard out using the `hypershift` CLI:

```shell
hypershift create kubeconfig
```

To delete the cluster and the infrastructure created earlier, run:

```shell
hypershift destroy cluster \
  --aws-creds ~/.aws/credentials \
  --namespace clusters \
  --name example
```

To destroy the IAM instance profile, run:
```shell
hypershift destroy iam aws --aws-creds ~/.aws/credentials
```

## How to add node pools to the example cluster

**Prerequisites:**

- An example cluster created using the _How to create a hosted cluster_ instructions

Use the `oc` tool to apply the YAML like the following to create additional node pools for the example hosted cluster:

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

With autoscaling:

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
