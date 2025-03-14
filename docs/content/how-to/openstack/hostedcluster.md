# Create a HostedCluster on OpenStack

Once all the [prerequisites](prerequisites.md) are met, it is now possible to create a Hosted Cluster on OpenStack.

Here are the available options specific to the OpenStack platform:

| Option                                  | Description                                                                                  | Required | Default       |
|-----------------------------------------|----------------------------------------------------------------------------------------------|----------|---------------|
| `--openstack-ca-cert-file`              | Path to the OpenStack CA certificate file                                                    | No       |               |
| `--openstack-cloud`                     | Name of the cloud in `clouds.yaml`                                                           | No       | `'openstack'` |
| `--openstack-credentials-file`          | Path to the OpenStack credentials file                                                       | No       |               |
| `--openstack-external-network-id`       | ID of the OpenStack external network                                                         | No       |               |
| `--openstack-ingress-floating-ip`       | A floating IP for OpenShift ingress                                                          | No       |               |
| `--openstack-node-additional-port`      | Attach additional ports to nodes. Params: `network-id`, `vnic-type`, `disable-port-security`, `address-pairs`. | No       |               |
| `--openstack-node-availability-zone`    | Availability zone for the nodepool                                                           | No       |               |
| `--openstack-node-flavor`               | Flavor for the nodepool                                                                      | Yes      |               |
| `--openstack-image-retention-policy`    | OpenStack Glance Image retention policy. Valid values are 'Orphan' and 'Prune'.              | No       | `Prune`       |
| `--openstack-node-image-name`           | Image name for the nodepool                                                                  | No       |               |
| `--openstack-dns-nameservers`           | List of DNS server addresses that will be provided when creating the subnet                  | No       |               |

Below is an example of how to create a cluster using environment variables and the `hcp` cli tool.

!!! note

    The `--release-image` flag could be used to provision the HostedCluster with a specific OpenShift Release (the hypershift operator has a support matrix of releases supported by a given version of the operator).

```shell
export CLUSTER_NAME=example
export BASE_DOMAIN=hypershift.lab
export PULL_SECRET="$HOME/pull-secret"
export WORKER_COUNT="2"

# OpenStack resources for the HostedCluster will be created
# in that project.
export OS_CLOUD="openstack"

# Flavor for the nodepool
export FLAVOR="m1.large"

# Pre-defined floating IP for Ingress
export INGRESS_FLOATING_IP="<ingress-floating-ip>"

# Optional flags:
# External network to use for the Ingress endpoint.
export EXTERNAL_NETWORK_ID="5387f86a-a10e-47fe-91c6-41ac131f9f30"

# CA certificate path to use for the OpenStack API if using self-signed certificates.
# In 4.18, this is not required as the CA cert found in clouds.yaml will be used.
export CA_CERT_PATH="$HOME/ca.crt"

# In 4.18, this is not required as the file will be discovered.
export CLOUDS_YAML="$HOME/clouds.yaml"

# SSH Key for the nodepool VMs
export SSH_KEY="$HOME/.ssh/id_rsa.pub"

# DNS nameserver for the subnet
export DNS_NAMESERVERS="1.1.1.1"

hcp create cluster openstack \
--name $CLUSTER_NAME \
--base-domain $BASE_DOMAIN \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--ssh-key $SSH_KEY \
--openstack-credentials-file $CLOUDS_YAML \
--openstack-ca-cert-file $CA_CERT_PATH \
--openstack-external-network-id $EXTERNAL_NETWORK_ID \
--openstack-node-flavor $FLAVOR \
--openstack-ingress-floating-ip $INGRESS_FLOATING_IP \
--openstack-dns-nameservers $DNS_NAMESERVERS
```

!!! note

    A default NodePool will be created for the cluster with 2 VM worker replicas
    per the `--node-pool-replicas` flag.

!!! note

    When using `hcp` CLI, High Availability will be enabled by default.
    Pods will be scheduled across different nodes to ensure that the control plane is highly available.
    When the management cluster worker nodes are spread across different availability zones,
    the hosted control plane will be spread across different availability zones as well in
    `PreferredDuringSchedulingIgnoredDuringExecution` mode for `PodAntiAffinity`.
    If your management cluster doesn't have enough workers (less than 3), which is not recommended nor supported,
    you'll need to specify the `--control-plane-availability-policy` flag to `SingleReplica`.

!!! note

    When not providing the `--openstack-node-image-name` flag, the latest RHCOS image will be used.
    ORC will handle the RHCOS image lifecycle by downloading the image from the OpenShift mirror and deleting it when it's no longer needed.
    If you want ORC to not delete the image, you can create the HostedCluster with the following option: `--openstack-image-retention-policy=Orphan`.
    This will prevent ORC from deleting the image resource.

After a few moments we should see our hosted control plane pods up and running:

```shell
oc -n clusters-$CLUSTER_NAME get pods

NAME                                                  READY   STATUS    RESTARTS   AGE
capi-provider-5cc7b74f47-n5gkr                        1/1     Running   0          3m
catalog-operator-5f799567b7-fd6jw                     2/2     Running   0          69s
certified-operators-catalog-784b9899f9-mrp6p          1/1     Running   0          66s
cluster-api-6bbc867966-l4dwl                          1/1     Running   0          66s
.
.
.
redhat-operators-catalog-9d5fd4d44-z8qqk              1/1     Running   0          66s
```

A guest cluster backed by OpenStack virtual machines typically takes around 10-15
minutes to fully provision.

## Accessing the HostedCluster

CLI access to the guest cluster is gained by retrieving the guest cluster's
kubeconfig. Below is an example of how to retrieve the guest cluster's
kubeconfig using the hcp cli.

```shell
hcp create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
```

If we access the cluster, we will see we have two nodes.

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get nodes

NAME                  STATUS   ROLES    AGE   VERSION
example-n6prw         Ready    worker   32m   v1.27.4+18eadca
example-nc6g4         Ready    worker   32m   v1.27.4+18eadca
```

We can also check the ClusterVersion:

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get clusterversion

NAME      VERSION       AVAILABLE   PROGRESSING   SINCE   STATUS
version   4.17.0        True        False         5m39s   Cluster version is 4.17.0
```

## Ingress and DNS (optional)

If you haven't created the HostedCluster with `--openstack-ingress-floating-ip`, you'll need to
update the DNS record with the floating IP address that was assigned to the `router-default` Service.

Once the workload cluster is deploying, the Ingress controller will be installed
and a router named `router-default` will be created in the `openshift-ingress` namespace.

You'll need to update your DNS with the external IP of that router so Ingress (and dependent operators like console) can work.

Once the HostedCluster is created, you need to wait for the `router-default` service to get an external IP:

```shell
oc -w --kubeconfig $CLUSTER_NAME-kubeconfig -n openshift-ingress get service/router-default -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
```

When the external IP exists, you can now create a DNS A record for `*.apps.<cluster-name>.<base-domain>` that matches the returned IP address.
Once this is done, the Ingress operator will become healthy and the console will be accessible shortly after.

!!! note

    The DNS propagation time can vary so you might need to wait a few minutes before your HostedCluster becomes healthy.

## Access to the guest cluster

Once the HostedCluster is healthy, you should be able to access the OpenShift console by navigating
to `https://console-openshift-console.apps.<cluster-name>.<base-domain>` in your browser.

To get the `kubeadmin` password, you can run this command:
```shell
oc get --namespace clusters Secret/${CLUSTER_NAME}-kubeadmin-password -o jsonpath='{.data.password}' | base64 --decode
```

To know whether the HostedCluster is healthy, you can verify with this command:
```shell
oc get --namespace clusters hostedclusters

NAME            VERSION   KUBECONFIG                       PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
example         4.17.0    example-admin-kubeconfig         Completed  True        False         The hosted control plane is available
```

## Scaling an existing NodePool

Manually scale a NodePool using the `oc scale` command:

```shell
NODEPOOL_NAME=$CLUSTER_NAME
NODEPOOL_REPLICAS=5

oc scale nodepool/$NODEPOOL_NAME --namespace clusters --replicas=$NODEPOOL_REPLICAS
```

After a while, in our hosted cluster this is what we will see:

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get nodes

NAME                  STATUS   ROLES    AGE     VERSION
example-9jvnf         Ready    worker   97s     v1.27.4+18eadca
example-n6prw         Ready    worker   116m    v1.27.4+18eadca
example-nc6g4         Ready    worker   117m    v1.27.4+18eadca
example-thp29         Ready    worker   4m17s   v1.27.4+18eadca
example-twxns         Ready    worker   88s     v1.27.4+18eadca
```

## OpenStack resources tagging

The OpenStack resources created by the CAPI provider are tagged with `openshiftClusterID=<infraID>`
but additional tags can be added to the resources via the `HostedCluster.Spec.Platform.OpenStack.Tags`
field when creating the HostedCluster with a given YAML file.
