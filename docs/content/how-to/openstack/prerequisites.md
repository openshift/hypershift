# Prerequisites for OpenStack

* Admin access to an OpenShift cluster (version 4.17+) specified by the `KUBECONFIG` environment variable.
  This cluster is referred to as the Management OCP cluster.
* The Management OCP cluster can be running on OpenStack (ShiftOnStack), but it could also be running on Baremetal or
  a public cloud such as AWS.
* The Management OCP cluster must be configured with OVNKubernetes as the default pod network CNI.
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`) must be installed.
* The `hcp` CLI must be installed and is the production tool to manage the hosted clusters.
* The `hypershift` CLI must be installed to deploy the HyperShift Operator. In production, it is not recommended to use that CLI to
  manage the hosted clusters.
* The HyperShift Operator must be installed in the Management OCP cluster.
* A load-balancer backend must be installed in the Management OCP cluster (e.g. Octavia) so the kube-api Service can be created for each Hosted Cluster.
* A valid [pull secret](https://console.redhat.com/openshift/install/platform-agnostic/user-provisioned) file for the `quay.io/openshift-release-dev` repository.
* OpenStack Octavia service must be running in the cloud hosting the guest cluster when ingress is configured with an Octavia load balancer.
  In the future, we'll explore other Ingress options like MetalLB.
* The default external network (on which the kube-apiserver LoadBalancer type service is created) of the Management OCP cluster must be reachable from the guest cluster.
* The RHCOS image must be uploaded to OpenStack (explained later).

## Install the HyperShift and HCP CLI

The `hcp` CLI tool is used to manage the creation and destruction of guest
clusters.

The `hypershift` CLI tool is a development tool that is used to install
developer builds of the HyperShift Operator.
The command below builds latest hypershift and hcp cli tools from source and
places the CLI tool within the `/usr/local/bin` directory.

!!! note

    The command below is the same if you use docker.
  
```shell
podman run --rm --privileged -it -v \
$PWD:/output docker.io/library/golang:1.22 /bin/bash -c \
'git clone https://github.com/openshift/hypershift.git && \
cd hypershift/ && \
make hypershift product-cli && \
mv bin/hypershift /output/hypershift && \
mv bin/hcp /output/hcp'

sudo install -m 0755 -o root -g root $PWD/hypershift /usr/local/bin/hypershift
sudo install -m 0755 -o root -g root $PWD/hcp /usr/local/bin/hcp
rm $PWD/hypershift
rm $PWD/hcp
```

## Deploy the HyperShift Operator

Use the hypershift cli tool to install the HyperShift operator into the
management cluster.

```shell
hypershift install --tech-preview-no-upgrade
```

!!! note

    HyperShift on OpenStack is possible behind a feature gate, which is why we have
    to install the operator with `--tech-preview-no-upgrade`. Once the platform
    is GA, the operator will be able to be installed without that flag.

Once installed, you should see the operator running in the `hypershift` namespace:

```shell
oc -n hypershift get pods

NAME                        READY   STATUS    RESTARTS   AGE
operator-755d587f44-lrtrq   1/1     Running   0          114s
operator-755d587f44-qj6pz   1/1     Running   0          114s
```

## Upload RHCOS image in OpenStack

For now, we need to manually push an RHCOS image that will be used when deploying the node pools
on OpenStack. In the [future](https://issues.redhat.com/browse/OSASINFRA-3492), the CAPI provider (CAPO) will handle the RHCOS image
lifecycle by using the image available in the chosen release payload.

Here is an example of how to upload an RHCOS image to OpenStack:

```shell
openstack image create --disk-format qcow2 --file rhcos-openstack.x86_64.qcow2 rhcos
```

!!! note

    The `rhcos-openstack.x86_64.qcow2` file is the RHCOS image that was downloaded from the OpenShift mirror.
    You can download the latest RHCOS image from the [Red Hat OpenShift Container Platform mirror](https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/).

## Create a floating IP for the Ingress (optional)

To get Ingress healthy in a HostedCluster without manual intervention, you need to create a floating IP that will be used by the Ingress service.

```shell
openstack floating ip create <external-network-id>
```

If you provide the floating IP to the `--openstack-ingress-floating-ip` flag without pre-creating it, cloud-provider-openstack will create it for you
only if the Neutron API policy allows a user to create floating IP with a specific IP address.

## Update the DNS record for the Ingress (optional)

If you use a pre-defined floating IP for ingress, you need to create a DNS record for the following wildcard domain that needs to point to the Ingress floating IP:
`*.apps.<cluster-name>.<base-domain>`