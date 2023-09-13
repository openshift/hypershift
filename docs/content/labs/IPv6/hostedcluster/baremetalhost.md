## Bare Metal Hosts

A **BareMetalHost** is an openshift-machine-api object that encompasses both physical and logical details, allowing it to be identified by the Metal3 operator. Subsequently, these details are associated with other Assisted Service objects known as Agents. The structure of this object is as follows:

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: hosted-ipv6-worker0-bmc-secret
  namespace: clusters-hosted-ipv6
data:
  password: YWRtaW4=
  username: YWRtaW4=
type: Opaque
---
apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
  name: hosted-ipv6-worker0
  namespace: clusters-hosted-ipv6
  labels:
    infraenvs.agent-install.openshift.io: hosted-ipv6
  annotations:
    inspect.metal3.io: disabled
    bmac.agent-install.openshift.io/hostname: hosted-ipv6-worker0
spec:
  automatedCleaningMode: disabled
  bmc:
    disableCertificateVerification: true
    address: redfish-virtualmedia://[192.168.125.1]:9000/redfish/v1/Systems/local/hosted-ipv6-worker0
    credentialsName: hosted-ipv6-worker0-bmc-secret
  bootMACAddress: aa:aa:aa:aa:03:11
  online: true
```

**Details**:

- We will have at least 1 secret that holds the BMH credentials, so we will need to create at least 2 objects per worker node.
- `spec.metadata.labels["infraenvs.agent-install.openshift.io"]` serves as the link between the Assisted Installer and the BareMetalHost objects.
- `spec.metadata.annotations["bmac.agent-install.openshift.io/hostname"]` represents the node name it will adopt during deployment.
- `spec.automatedCleaningMode` prevents the node from being erased by the Metal3 operator.
- `spec.bmc.disableCertificateVerification` is set to `true` to bypass certificate validation from the client.
- `spec.bmc.address` denotes the BMC address of the worker node.
- `spec.bmc.credentialsName` points to the Secret where User/Password credentials are stored.
- `spec.bootMACAddress` indicates the interface MACAddress from which the node will boot.
- `spec.online` defines the desired state of the node once the BMH object is created.

To deploy this object, simply follow the same procedure as before:

!!! important

    Please create the virtual machines before you create the BareMetalHost and the destination Nodes.

To deploy the BareMetalHost object, execute the following command:

```bash
oc apply -f 04-bmh.yaml
```

This will be the process:

- Preparing (Trying to reach the nodes):
```
NAMESPACE         NAME             STATE         CONSUMER   ONLINE   ERROR   AGE
clusters-hosted   hosted-worker0   registering              true             2s
clusters-hosted   hosted-worker1   registering              true             2s
clusters-hosted   hosted-worker2   registering              true             2s
```

- Provisioning (Nodes Booting up)
```
NAMESPACE         NAME             STATE          CONSUMER   ONLINE   ERROR   AGE
clusters-hosted   hosted-worker0   provisioning              true             16s
clusters-hosted   hosted-worker1   provisioning              true             16s
clusters-hosted   hosted-worker2   provisioning              true             16s
```

- Provisioned (Nodes Booted up successfully)
```
NAMESPACE         NAME             STATE         CONSUMER   ONLINE   ERROR   AGE
clusters-hosted   hosted-worker0   provisioned              true             67s
clusters-hosted   hosted-worker1   provisioned              true             67s
clusters-hosted   hosted-worker2   provisioned              true             67s
```

## Agents registration

After the nodes have booted up, you will observe the appearance of agents within the namespace.

```
NAMESPACE         NAME                                   CLUSTER   APPROVED   ROLE          STAGE
clusters-hosted   aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0411             true       auto-assign
clusters-hosted   aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0412             true       auto-assign
clusters-hosted   aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0413             true       auto-assign
```

These agents represent the nodes available for installation. To assign them to a HostedCluster, scale up the NodePool.

## Scaling Up the Nodepool

Once we have the BareMetalHosts created, the statuses of these BareMetalHosts will transition from `Registering` (Attempting to reach the Node's BMC) to `Provisioning` (Node Booting Up), and finally to `Provisioned` (Successful node boot-up).

The nodes will boot with the Agent's RHCOS LiveISO and a default pod named "agent." This agent is responsible for receiving instructions from the Assisted Service Operator to install the Openshift payload.

To accomplish this, execute the following command:

```bash
oc -n clusters scale nodepool hosted-ipv6 --replicas 3
```

After the NodePool scaling, you will notice that the agents are assigned to a Hosted Cluster.

```
NAMESPACE         NAME                                   CLUSTER   APPROVED   ROLE          STAGE
clusters-hosted   aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0211   hosted    true       auto-assign
clusters-hosted   aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0212   hosted    true       auto-assign
clusters-hosted   aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0213   hosted    true       auto-assign
```

And the NodePool replicas set

```
NAMESPACE   NAME     CLUSTER   DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION                              UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
clusters    hosted   hosted    3                               False         False        4.14.0-0.nightly-2023-08-29-102237                                      Minimum availability requires 3 replicas, current 0 available
```

So now, we need to wait until the nodes join the cluster. The Agents will provide updates on their current stage and status. Initially, they may not post any status, but eventually, they will.