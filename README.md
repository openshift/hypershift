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
- An SSH public key file for guest node access
- An [AWS credentials file](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html) with permissions to create infrastructure for the cluster

Run the `hypershift` command to create an IAM instance profile for your workers:
```shell
hypershift create iam aws --aws-creds /my/aws-credentials
```
NOTE: The default profile name is `hypershift-worker-profile`. To use a different name (for example, in a shared account), use the `--profile-name` flag. The worker instance profile only needs to be created once per account and you can reuse it as needed for your clusters.

Run the `hypershift` command to create cloud infrastructure for your cluster:
NOTE: Infrastructure for a cluster can be created once and reused. However it should only correspond to one cluster at a time.
```shell
hypershift create infra aws --aws-creds /my/aws-credentials --infra-id INFRA-ID --region us-east-2 --output-file /tmp/infra.json
```
For `INFRA-ID` use a short identifier for your cluster such as `mycluster-1234`. It should be unique in your AWS account.
For region, the default region is `us-east-1`, specify a different region if desired.
The output file will contain JSON with the details of your provisioned infrastructure.

Run the `hypershift` command to generate and install the example cluster:

```shell
hypershift create cluster \
  --pull-secret /my/pull-secret \
  --aws-creds /my/aws-credentials \
  --ssh-key /my/ssh-public-key \
  --infra-json /tmp/infra.json
```
NOTE: The file specified in the `--infra-json` flag should be the same file you created with the `create infra aws` command above.
If you created an instance profile named something other than `hypershift-worker-profile`, you need to pass the profile name with the `--instance-profile` flag.

Eventually the cluster's kubeconfig will become available and can be fetched and decoded locally:

```shell
oc get secret \
  --template={{.data.kubeconfig}} \
  --namespace clusters \
  example-admin-kubeconfig | base64 --decode
```

To delete the cluster, run:

```shell
oc delete --namespace clusters
```

NOTE: After deleting the cluster, you can use an existing `infra.json` to create a new cluster.

To destroy your AWS infrastructure:
```shell
hypershift destroy infra aws --aws-creds /my/aws/credentials --infra-id INFRA-ID --region us-east-2
```
Specify the same INFRA-ID and region as your original `create infra` command.

To destroy the IAM instance profile:
```shell
hypershift destroy iam aws --aws-creds /my/aws-credentials
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
