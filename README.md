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
- An [AWS credentials file](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html) with permissions to create infrastructure for the cluster

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
- A valid [pull secret](https://cloud.redhat.com/openshift/install/aws/installer-provisioned) file for the `quay.io/openshift-release-dev` repository
- A Route53 public zone for the `base-domain`

Run the `hypershift` command to create a cluster named `example` in the `clusters`
namespace, including the cloud infrastructure to support it.

```shell
hypershift create cluster \
  --pull-secret /my/pull-secret \
  --aws-creds ~/.aws/credentials \
  --name example \
  --base-domain hypershift.example.com
```

After a few minutes, check the `hostedclusters` in the `clusters` namespace and when ready it will look similar to
the following:

```shell
$ oc get hostedclusters -n clusters
NAME      VERSION   KUBECONFIG                 AVAILABLE
example   4.7.6     example-admin-kubeconfig   True
```

Also check the `pods` in the `clusters-example` namespace

```shell
$ oc get pods -n clusters-example
NAME                                              READY   STATUS      RESTARTS   AGE
capa-controller-manager-b6b49f696-j9q7l           1/1     Running     0          53m
cluster-api-7fd76bbb57-6kv6z                      1/1     Running     0          53m
cluster-autoscaler-dcc8d8795-llss8                1/1     Running     5          52m
cluster-policy-controller-7c89bc799c-4b6xt        1/1     Running     0          52m
cluster-version-operator-959994fb6-v97w5          1/1     Running     0          52m
control-plane-operator-5cb584b6fc-hnrcn           1/1     Running     0          53m
etcd-l5zk9cp856                                   1/1     Running     0          52m
etcd-operator-68ffbf98fd-wmcrx                    1/1     Running     0          52m
hosted-cluster-config-operator-65b656d96f-cfzqk   1/1     Running     1          52m
kube-apiserver-6c89cfc4dd-t5zfn                   3/3     Running     0          52m
kube-controller-manager-5576c4c8f4-rrxn8          1/1     Running     0          46m
kube-scheduler-776774ff68-fcsrf                   1/1     Running     0          52m
machine-config-server-example-596cdc68fb-5kr4t    1/1     Running     0          53m
manifests-bootstrapper                            0/1     Completed   3          52m
oauth-openshift-75879b669f-grmhf                  1/1     Running     0          51m
openshift-apiserver-766d9c6f79-c442c              1/1     Running     0          51m
openshift-controller-manager-6db5976f8f-4nhgb     1/1     Running     0          52m
openshift-oauth-apiserver-fb9d8c45b-7xzlp         1/1     Running     2          52m
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

## How to add additional node pools to the example cluster

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
    type: AWS
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.8.0-rc.0-x86_64
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
    type: AWS
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.8.0-rc.0-x86_64
```
## Troubleshooting

**Pull Secret Issues**

- If you run into an issue where the `pods` are not creating properly when you
issue the `hypershift create cluster ...` command, it may be your `pull-secret`. There may be a
typo or a bad copy/paste that has left the `pull-secret` malformed. Make sure the `pull-secret` is accurate and exists
on your system and you properly pass in the path to the file. A proper representation of the `pull-secret`
will look like the following in this example:

```shell
$ oc get secret example-pull-secret -n clusters
NAME                  TYPE     DATA   AGE
example-pull-secret   Opaque   1      74m

$ oc get secret pull-secret -n clusters-example
NAME          TYPE                             DATA   AGE
pull-secret   kubernetes.io/dockerconfigjson   1      74m

```

- If you do not see the `pull-secret` above and see an error similar to below, then you most likely have a malformed
`pull-secret` that you passed into the `hypershift create cluster...` command:

```shell
$ oc logs deploy/control-plane-operator -n clusters-example

...
"error": "failed to ensure control plane: failed to get pull secret pull-secret: secrets \"pull-secret\" not found"}
...
```

- If your `pull-secret` was not properly created and you issue the `hypershift destroy cluster ...` command, it does not
clean up the `clusters/secrets` - so if that was malformed or not accurate, you will need to fix and manually delete
that secret before you rerun the `hypershift create cluster ...` command.

```shell
$ hypershift destroy cluster   --aws-creds ~/.aws/credentials   --namespace clusters   --name example

$ oc delete secret example-pull-secret -n clusters

$ hypershift create cluster --pull-secret ~/pull-secret \
      --aws-creds ~/.aws/credentials \
      --name example \
      --base-domain yourroute53domain
```
