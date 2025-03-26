# Create an Agent cluster

This document explains how to create HostedClusters and NodePools using the Agent platform.

The Agent platform uses the [Infrastructure Operator](https://github.com/openshift/assisted-service) (AKA Assisted Installer) to add
worker nodes to a hosted cluster. For a primer on the Infrastructure Operator, see
[here](https://github.com/openshift/assisted-service/blob/master/docs/hive-integration/kube-api-getting-started.md).

## Overview

When you create a HostedCluster with the Agent platform, HyperShift will install the [Agent CAPI
provider](https://github.com/openshift/cluster-api-provider-agent) in the Hosted Control Plane (HCP) namespace.

Upon scaling up a NodePool, a Machine will be created, and the CAPI provider will find a suitable Agent to match this Machine.
Suitable means that the Agent is approved, is passing validations, is not currently bound (in use), and has the requirements
specified on the NodePool Spec (e.g., minimum CPU/RAM, labels matching the label selector). You may monitor the installation of an
Agent by checking its `Status` and `Conditions`.

Upon scaling down a NodePool, Agents will be unbound from the corresponding cluster. However, you must boot them with the Discovery
Image once again before reusing them.

## Install HyperShift Operator

Before installing the HyperShift operator we need to get the HyperShift CLI. We have two methods for getting the CLI installed in our system.

### Method 1 - Build the HyperShift CLI

Follow instructions for building the HyperShift CLI in [Getting
Started](https://hypershift-docs.netlify.app/getting-started/#prerequisites)

### Method 2 - Extract HyperShift CLI from the Operator Image

> **INFO:** We are using Podman in the example, same applies to Docker.

~~~sh
export HYPERSHIFT_RELEASE=4.11

podman cp $(podman create --name hypershift --rm --pull always quay.io/hypershift/hypershift-operator:${HYPERSHIFT_RELEASE}):/usr/bin/hypershift /tmp/hypershift && podman rm -f hypershift

sudo install -m 0755 -o root -g root /tmp/hypershift /usr/local/bin/hypershift
~~~

### Deploy the HyperShift Operator

With the CLI deployed, we can go ahead and deploy the operator:

> **WARN:** If we don't define the HyperShift image we want to use, by default the CLI will deploy `latest`. Usually you want to deploy the image matching the release of the OpenShift cluster where HyperShift will run.

~~~sh
# This install latest
hypershift install
# You may want to run this instead
hypershift install --hypershift-image quay.io/hypershift/hypershift-operator:4.11
~~~

You will see the operator running in the `hypershift` namespace:

~~~sh
oc -n hypershift get pods

NAME                      READY   STATUS    RESTARTS   AGE
operator-55fffbd6-whkxs   1/1     Running   0          61s
~~~

## Install Assisted Service and Hive Operators

> **NOTE**: If Red Hat Advanced Cluster Management (RHACM) is already installed, this can be skipped as the Infrastructure Operator
> and Hive Operator are dependencies of RHACM.

We will leverage [`tasty`](https://github.com/karmab/tasty) to deploy the required operators easily.

Install tasty:

~~~sh
curl -s -L https://github.com/karmab/tasty/releases/download/v0.4.0/tasty-linux-amd64 > ./tasty
sudo install -m 0755 -o root -g root ./tasty /usr/local/bin/tasty
~~~

Install the operators

~~~sh
tasty install assisted-service-operator hive-operator
~~~

## Configure Agent Service

Create the `AgentServiceConfig` resource

~~~sh
export DB_VOLUME_SIZE="10Gi"
export FS_VOLUME_SIZE="10Gi"
export OCP_VERSION="4.11.5"
export OCP_MAJMIN=${OCP_VERSION%.*}
export ARCH="x86_64"
export OCP_RELEASE_VERSION=$(curl -s https://mirror.openshift.com/pub/openshift-v4/${ARCH}/clients/ocp/${OCP_VERSION}/release.txt | awk '/machine-os / { print $2 }')
export ISO_URL="https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/${OCP_MAJMIN}/${OCP_VERSION}/rhcos-${OCP_VERSION}-${ARCH}-live.${ARCH}.iso"
export ROOT_FS_URL="https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/${OCP_MAJMIN}/${OCP_VERSION}/rhcos-${OCP_VERSION}-${ARCH}-live-rootfs.${ARCH}.img"

envsubst <<"EOF" | oc apply -f -
apiVersion: agent-install.openshift.io/v1beta1
kind: AgentServiceConfig
metadata:
 name: agent
spec:
  databaseStorage:
    accessModes:
    - ReadWriteOnce
    resources:
      requests:
        storage: ${DB_VOLUME_SIZE}
  filesystemStorage:
    accessModes:
    - ReadWriteOnce
    resources:
      requests:
        storage: ${FS_VOLUME_SIZE}
  osImages:
    - openshiftVersion: "${OCP_VERSION}"
      version: "${OCP_RELEASE_VERSION}"
      url: "${ISO_URL}"
      rootFSUrl: "${ROOT_FS_URL}"
      cpuArchitecture: "${ARCH}"
EOF
~~~

## Configure DNS

The API Server for the Hosted Cluster is exposed a Service of type NodePort.

A DNS entry must exist for `api.${HOSTED_CLUSTER_NAME}.${BASEDOMAIN}` pointing to destination where the API Server can be reached.

This can be as simple as an A record pointing to one of the nodes in the management cluster (i.e. the cluster running the HCP).  It can also point to a [load balancer](https://docs.openshift.com/container-platform/4.11/installing/installing_platform_agnostic/installing-platform-agnostic.html#installation-load-balancing-user-infra-example_installing-platform-agnostic) deployed to redirect incoming traffic to the ingress pods.

### Example DNS Config

~~~conf
api.example.krnl.es.    IN A 192.168.122.20
api.example.krnl.es.    IN A 192.168.122.21
api.example.krnl.es.    IN A 192.168.122.22
api-int.example.krnl.es.    IN A 192.168.122.20
api-int.example.krnl.es.    IN A 192.168.122.21
api-int.example.krnl.es.    IN A 192.168.122.22
*.apps.example.krnl.es. IN A 192.168.122.23
~~~

## Create a Hosted Cluster

> **WARN:** Make sure you have a default storage class configured for your cluster, otherwise you may end up with pending PVCs.

~~~sh
export CLUSTERS_NAMESPACE="clusters"
export HOSTED_CLUSTER_NAME="example"
export HOSTED_CONTROL_PLANE_NAMESPACE="${CLUSTERS_NAMESPACE}-${HOSTED_CLUSTER_NAME}"
export BASEDOMAIN="krnl.es"
export PULL_SECRET_FILE=$PWD/pull-secret
export OCP_RELEASE=4.11.5-x86_64
export MACHINE_CIDR=192.168.122.0/24
# Typically the namespace is created by the hypershift-operator
# but agent cluster creation generates a capi-provider role that
# needs the namespace to already exist
oc create ns ${HOSTED_CONTROL_PLANE_NAMESPACE}

hypershift create cluster agent \
    --name=${HOSTED_CLUSTER_NAME} \
    --pull-secret=${PULL_SECRET_FILE} \
    --agent-namespace=${HOSTED_CONTROL_PLANE_NAMESPACE} \
    --base-domain=${BASEDOMAIN} \
    --api-server-address=api.${HOSTED_CLUSTER_NAME}.${BASEDOMAIN} \
    --release-image=quay.io/openshift-release-dev/ocp-release:${OCP_RELEASE}
~~~

After a few moments we should see our hosted control plane pods up and running:

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get pods

NAME                                             READY   STATUS    RESTARTS   AGE
capi-provider-7dcf5fc4c4-nr9sq                   1/1     Running   0          4m32s
catalog-operator-6cd867cc7-phb2q                 2/2     Running   0          2m50s
certified-operators-catalog-884c756c4-zdt64      1/1     Running   0          2m51s
cluster-api-f75d86f8c-56wfz                      1/1     Running   0          4m32s
cluster-autoscaler-7977864686-2rz4c              1/1     Running   0          4m13s
cluster-network-operator-754cf4ffd6-lwfm2        1/1     Running   0          2m51s
cluster-policy-controller-784f995d5-7cbrz        1/1     Running   0          2m51s
cluster-version-operator-5c68f7f4f8-lqzcm        1/1     Running   0          2m51s
community-operators-catalog-58599d96cd-vpj2v     1/1     Running   0          2m51s
control-plane-operator-f6b4c8465-4k5dh           1/1     Running   0          4m32s
etcd-0                                           1/1     Running   0          4m13s
hosted-cluster-config-operator-c4776f89f-dt46j   1/1     Running   0          2m51s
ignition-server-7cd8676fc5-hjx29                 1/1     Running   0          4m22s
ingress-operator-75484cdc8c-zhdz5                1/2     Running   0          2m51s
konnectivity-agent-c5485c9df-jsm9s               1/1     Running   0          4m13s
konnectivity-server-85dc754888-7z8vm             1/1     Running   0          4m13s
kube-apiserver-db5fb5549-zlvpq                   3/3     Running   0          4m13s
kube-controller-manager-5fbf7b7b7b-mrtjj         1/1     Running   0          90s
kube-scheduler-776c59d757-kfhv6                  1/1     Running   0          3m12s
machine-approver-c6b947895-lkdbk                 1/1     Running   0          4m13s
oauth-openshift-787b87cff6-trvd6                 2/2     Running   0          87s
olm-operator-69c4657864-hxwzk                    2/2     Running   0          2m50s
openshift-apiserver-67f9d9c5c7-c9bmv             2/2     Running   0          89s
openshift-controller-manager-5899fc8778-q89xh    1/1     Running   0          2m51s
openshift-oauth-apiserver-569c78c4d-568v8        1/1     Running   0          2m52s
packageserver-ddfffb8d7-wlz6l                    2/2     Running   0          2m50s
redhat-marketplace-catalog-7dd77d896-jtxkd       1/1     Running   0          2m51s
redhat-operators-catalog-d66b5c965-qwhn7         1/1     Running   0          2m51s
~~~

## Create an InfraEnv

An InfraEnv is a environment to which hosts booting the live ISO can join as Agents.  In this case, the Agents will be created in the
same namespace as our HostedControlPlane.

~~~sh
export SSH_PUB_KEY=$(cat $HOME/.ssh/id_rsa.pub)

envsubst <<"EOF" | oc apply -f -
apiVersion: agent-install.openshift.io/v1beta1
kind: InfraEnv
metadata:
  name: ${HOSTED_CLUSTER_NAME}
  namespace: ${HOSTED_CONTROL_PLANE_NAMESPACE}
spec:
  pullSecretRef:
    name: pull-secret
  sshAuthorizedKey: ${SSH_PUB_KEY}
EOF
~~~

This will generate a live ISO that allows machines (VMs or bare-metal) to join as Agents.

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get InfraEnv ${HOSTED_CLUSTER_NAME} -ojsonpath="{.status.isoDownloadURL}"
~~~

## Adding Agents

You can add Agents by manually configuring the machine to boot with the live ISO or by using Metal3.

### Manual

The live ISO may be downloaded and used to boot a node (bare-metal or VM).

On boot, the node will communicate with the assisted-service and register as an Agent in the same namespace as the InfraEnv.

Once each Agent is created, optionally set its installation_disk_id and hostname in the Spec. Then approve it to indicate that the Agent is ready for use.

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get agents

NAME                                   CLUSTER   APPROVED   ROLE          STAGE
86f7ac75-4fc4-4b36-8130-40fa12602218                        auto-assign
e57a637f-745b-496e-971d-1abbf03341ba                        auto-assign
~~~

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} patch agent 86f7ac75-4fc4-4b36-8130-40fa12602218 -p '{"spec":{"installation_disk_id":"/dev/sda","approved":true,"hostname":"worker-0.example.krnl.es"}}' --type merge

oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} patch agent 23d0c614-2caa-43f5-b7d3-0b3564688baa -p '{"spec":{"installation_disk_id":"/dev/sda","approved":true,"hostname":"worker-1.example.krnl.es"}}' --type merge
~~~

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get agents

NAME                                   CLUSTER   APPROVED   ROLE          STAGE
86f7ac75-4fc4-4b36-8130-40fa12602218             true       auto-assign
e57a637f-745b-496e-971d-1abbf03341ba             true       auto-assign
~~~

### Metal3

We will leverage the Assisted Service and Hive to create the custom ISO as well as the Baremetal Operator to perform the installation.

> **WARN:** Since the `BaremetalHost` objects will be created outside the baremetal-operator namespace we need to configure the operator to watch all namespaces.

~~~sh
oc patch provisioning provisioning-configuration --type merge -p '{"spec":{"watchAllNamespaces": true }}'
~~~

> **INFO:** This will trigger a restart of the `metal3` pod in the `openshift-machine-api` namespace.

* Wait until the `metal3` pod is ready again:

~~~sh
until oc wait -n openshift-machine-api $(oc get pods -n openshift-machine-api -l baremetal.openshift.io/cluster-baremetal-operator=metal3-state -o name) --for condition=containersready --timeout 10s >/dev/null 2>&1 ; do sleep 1 ; done
~~~

Now we can go ahead and create our BaremetalHost objects. We will need to configure some variables required to be able to boot our bare-metal nodes.

* `BMC_USERNAME`: Username to be used for connecting to the BMC.
* `BMC_PASSWORD`: Password to be used for connecting to the BMC.
* `BMC_IP`: IP used by Metal3 to connect to the BMC.
* `WORKER_NAME`: Name of the BaremetalHost object (this will be used as hostname as well)
* `BOOT_MAC_ADDRESS`: MAC address of the NIC connected to the MachineNetwork.
* `UUID`: Redfish UUID, this is usually `1`. If using sushy-tools this will be a long UUID. If using iDrac this will be `System.Embedded.1`. You may need to check with the vendor.
* `REDFISH_SCHEME`: The Redfish provider to use. If using hardware that uses a standard Redfish implementation you can set this to `redfish-virtualmedia`. iDRAC will use `idrac-virtualmedia`. iLO5 will use `ilo5-virtualmedia`. You may need to check with the vendor.
* `REDFISH`: Redfish connection endpoint.

~~~sh
export BMC_USERNAME=$(echo -n "root" | base64 -w0)
export BMC_PASSWORD=$(echo -n "calvin" | base64 -w0)
export BMC_IP="192.168.124.228"
export WORKER_NAME="ocp-worker-0"
export BOOT_MAC_ADDRESS="aa:bb:cc:dd:ee:ff"
export UUID="1"
export REDFISH_SCHEME="redfish-virtualmedia"
export REDFISH="${REDFISH_SCHEME}://${BMC_IP}/redfish/v1/Systems/${UUID}"
~~~

With the required information ready, let's create the BaremetalHost. First we will create the BMC Secret:

~~~sh
envsubst <<"EOF" | oc apply -f -
apiVersion: v1
data:
  password: ${BMC_PASSWORD}
  username: ${BMC_USERNAME}
kind: Secret
metadata:
  name: ${WORKER_NAME}-bmc-secret
  namespace: ${HOSTED_CONTROL_PLANE_NAMESPACE}
type: Opaque
EOF
~~~

Second, we will create the BMH:

> **INFO:** `infraenvs.agent-install.openshift.io` label is used to specify which InfraEnv is used to boot the BMH. `bmac.agent-install.openshift.io/hostname` is used to manually set a hostname.

In case you want to manually specify the installation disk you can make use of the [rootDeviceHints](https://github.com/metal3-io/baremetal-operator/blob/main/docs/api.md#rootdevicehints) in the BMH Spec. If rootDeviceHints are not provided, the agent will pick the installation disk that better suits the installation requirements.

~~~sh
envsubst <<"EOF" | oc apply -f -
apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
  name: ${WORKER_NAME}
  namespace: ${HOSTED_CONTROL_PLANE_NAMESPACE}
  labels:
    infraenvs.agent-install.openshift.io: ${HOSTED_CLUSTER_NAME}
  annotations:
    inspect.metal3.io: disabled
    bmac.agent-install.openshift.io/hostname: ${WORKER_NAME}
spec:
  automatedCleaningMode: disabled
  bmc:
    disableCertificateVerification: True
    address: ${REDFISH}
    credentialsName: ${WORKER_NAME}-bmc-secret
  bootMACAddress: ${BOOT_MAC_ADDRESS}
  online: true
EOF
~~~

The Agent should be automatically approved, if not, make sure the `bootMACAddress` is correct.

The BMH will be provisioned:

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get bmh

NAME           STATE          CONSUMER   ONLINE   ERROR   AGE
ocp-worker-0   provisioning              true             2m50s
~~~

BMH will reach `provisioned` state eventually.

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get bmh
NAME           STATE          CONSUMER   ONLINE   ERROR   AGE
ocp-worker-0   provisioned               true             72s
~~~

Provisioned means that the node was configured to boot from the virtualCD properly. It will take a few moments for the Agent to show up:

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get agent

NAME                                   CLUSTER   APPROVED   ROLE          STAGE
4dac1ab2-7dd5-4894-a220-6a3473b67ee6             true       auto-assign  
~~~

As you can see it was auto-approved. We will repeat this with another two nodes.

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get agent

NAME                                   CLUSTER   APPROVED   ROLE          STAGE
4dac1ab2-7dd5-4894-a220-6a3473b67ee6             true       auto-assign  
d9198891-39f4-4930-a679-65fb142b108b             true       auto-assign
da503cf1-a347-44f2-875c-4960ddb04091             true       auto-assign
~~~

## Accessing the HostedCluster

We have the HostedControlPlane running and the Agents ready to join the HostedCluster. Before we join the Agents let's access the HostedCluster.

First, we need to generate the kubeconfig:

~~~sh
hypershift create kubeconfig --namespace ${CLUSTERS_NAMESPACE} --name ${HOSTED_CLUSTER_NAME} > ${HOSTED_CLUSTER_NAME}.kubeconfig
~~~

If we access the cluster we will see that we don't have any nodes and that the ClusterVersion is trying to reconcile the OCP release:

~~~sh
oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig get clusterversion,nodes

NAME                                         VERSION   AVAILABLE   PROGRESSING   SINCE   STATUS
clusterversion.config.openshift.io/version             False       True          8m6s    Unable to apply 4.11.5: some cluster operators have not yet rolled out
~~~

In order to get the cluster in a running state we need to add some nodes to it. Let's do it.

## Scale the NodePool

We add nodes to our HostedCluster by scaling the NodePool object. In this case we will start by scaling the NodePool object to two nodes:

~~~sh
oc -n ${CLUSTERS_NAMESPACE} scale nodepool ${NODEPOOL_NAME} --replicas 2
~~~

The ClusterAPI Agent provider will pick two agents randomly that will get assigned to the HostedCluster. These agents will go over different states and will finally join the HostedCluster as OpenShift nodes.

> **INFO:** States will be `binding` -> `discoverying` -> `insufficient` -> `installing` -> `installing-in-progress` -> `added-to-existing-cluster`

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get agent

NAME                                   CLUSTER         APPROVED   ROLE          STAGE
4dac1ab2-7dd5-4894-a220-6a3473b67ee6   hypercluster1   true       auto-assign  
d9198891-39f4-4930-a679-65fb142b108b                   true       auto-assign  
da503cf1-a347-44f2-875c-4960ddb04091   hypercluster1   true       auto-assign

oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get agent -o jsonpath='{range .items[*]}BMH: {@.metadata.labels.agent-install\.openshift\.io/bmh} Agent: {@.metadata.name} State: {@.status.debugInfo.state}{"\n"}{end}'

BMH: ocp-worker-2 Agent: 4dac1ab2-7dd5-4894-a220-6a3473b67ee6 State: binding
BMH: ocp-worker-0 Agent: d9198891-39f4-4930-a679-65fb142b108b State: known-unbound
BMH: ocp-worker-1 Agent: da503cf1-a347-44f2-875c-4960ddb04091 State: insufficient
~~~

Once the agents have reached the `added-to-existing-cluster` state, we should see the OpenShift nodes after a few moments:

~~~sh
oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig get nodes

NAME           STATUS   ROLES    AGE     VERSION
ocp-worker-1   Ready    worker   5m41s   v1.24.0+3882f8f
ocp-worker-2   Ready    worker   6m3s    v1.24.0+3882f8f
~~~

At this point some ClusterOperators will start to reconcile by adding workloads to the nodes.

We can also see that two Machines were created when we scaled up the NodePool:

~~~sh
oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get machines

NAME                            CLUSTER               NODENAME       PROVIDERID                                     PHASE     AGE   VERSION
hypercluster1-c96b6f675-m5vch   hypercluster1-b2qhl   ocp-worker-1   agent://da503cf1-a347-44f2-875c-4960ddb04091   Running   15m   4.11.5
hypercluster1-c96b6f675-tl42p   hypercluster1-b2qhl   ocp-worker-2   agent://4dac1ab2-7dd5-4894-a220-6a3473b67ee6   Running   15m   4.11.5
~~~

At some point the clusterversion reconcile will reach a point where only Ingress and Console cluster operators will be missing:

~~~sh
oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig get clusterversion,co

NAME                                         VERSION   AVAILABLE   PROGRESSING   SINCE   STATUS
clusterversion.config.openshift.io/version             False       True          40m     Unable to apply 4.11.5: the cluster operator console has not yet successfully rolled out

NAME                                                                           VERSION   AVAILABLE   PROGRESSING   DEGRADED   SINCE   MESSAGE
clusteroperator.config.openshift.io/console                                    4.11.5    False       False         False      11m     RouteHealthAvailable: failed to GET route (https://console-openshift-console.apps.hypercluster1.domain.com): Get "https://console-openshift-console.apps.hypercluster1.domain.com": dial tcp 10.19.3.29:443: connect: connection refused
clusteroperator.config.openshift.io/csi-snapshot-controller                    4.11.5    True        False         False      10m  
clusteroperator.config.openshift.io/dns                                        4.11.5    True        False         False      9m16s  
clusteroperator.config.openshift.io/image-registry                             4.11.5    True        False         False      9m5s  
clusteroperator.config.openshift.io/ingress                                    4.11.5    True        False         True       39m     The "default" ingress controller reports Degraded=True: DegradedConditions: One or more other status conditions indicate a degraded state: CanaryChecksSucceeding=False (CanaryChecksRepetitiveFailures: Canary route checks for the default ingress controller are failing)
clusteroperator.config.openshift.io/insights                                   4.11.5    True        False         False      11m  
clusteroperator.config.openshift.io/kube-apiserver                             4.11.5    True        False         False      40m  
clusteroperator.config.openshift.io/kube-controller-manager                    4.11.5    True        False         False      40m  
clusteroperator.config.openshift.io/kube-scheduler                             4.11.5    True        False         False      40m  
clusteroperator.config.openshift.io/kube-storage-version-migrator              4.11.5    True        False         False      10m  
clusteroperator.config.openshift.io/monitoring                                 4.11.5    True        False         False      7m38s  
clusteroperator.config.openshift.io/network                                    4.11.5    True        False         False      11m  
clusteroperator.config.openshift.io/openshift-apiserver                        4.11.5    True        False         False      40m  
clusteroperator.config.openshift.io/openshift-controller-manager               4.11.5    True        False         False      40m  
clusteroperator.config.openshift.io/openshift-samples                          4.11.5    True        False         False      8m54s  
clusteroperator.config.openshift.io/operator-lifecycle-manager                 4.11.5    True        False         False      40m  
clusteroperator.config.openshift.io/operator-lifecycle-manager-catalog         4.11.5    True        False         False      40m  
clusteroperator.config.openshift.io/operator-lifecycle-manager-packageserver   4.11.5    True        False         False      40m  
clusteroperator.config.openshift.io/service-ca                                 4.11.5    True        False         False      11m  
clusteroperator.config.openshift.io/storage                                    4.11.5    True        False         False      11m
~~~

Let's fix the Ingress.

## Handling Ingress

Every OpenShift cluster comes set up with a default application ingress
controller, which is expected have an external DNS record associated with it.

For example, if a HyperShift cluster named `example` with the base domain
`krnl.es` is created, then the wildcard domain
`*.apps.example.krnl.es` is expected to be routable.

### Set up a LoadBalancer and wildcard DNS record for the `*.apps`.

This option requires deploying MetalLB, configuring a new LoadBalancer service that routes to the ingress deployment, as well as assigning a wildcard DNS entry to the LoadBalancer's IP address.

#### Step 1 - Get the MetalLB Operator Deployed

Set up [MetalLB](https://docs.openshift.com/container-platform/4.10/networking/metallb/about-metallb.html) so that when you create a service of type LoadBalancer, MetalLB will add an external IP address for the service.

~~~sh
cat <<"EOF" | oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig apply -f -
---
apiVersion: v1
kind: Namespace
metadata:
  name: metallb
  labels:
    openshift.io/cluster-monitoring: "true"
  annotations:
    workload.openshift.io/allowed: management
---
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: metallb-operator-operatorgroup
  namespace: metallb
---
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: metallb-operator
  namespace: metallb
spec:
  channel: "stable"
  name: metallb-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
~~~

Once the operator is up and running, create the MetalLB instance:

~~~sh
cat <<"EOF" | oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig apply -f -
apiVersion: metallb.io/v1beta1
kind: MetalLB
metadata:
  name: metallb
  namespace: metallb
EOF
~~~

#### Step 2 - Get the MetalLB Operator Configured

We will create an `IPAddressPool` with a single IP address and L2Advertisement to advertise the LoadBalancer IPs provided by the `IPAddressPool` via L2.
Since layer 2 mode relies on ARP and NDP, the IP address must be on the same subnet as the network used by the cluster nodes in order for the MetalLB to work.
more information about metalLB configuration options is available [here](https://metallb.universe.tf/configuration/).
> **WARN:** Change `INGRESS_IP` env var to match your environments addressing.

~~~sh
export INGRESS_IP=192.168.122.23

envsubst <<"EOF" | oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig apply -f -
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: ingress-public-ip
  namespace: metallb
spec:
  protocol: layer2
  autoAssign: false
  addresses:
    - ${INGRESS_IP}-${INGRESS_IP}
---

apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: ingress-public-ip
  namespace: metallb
EOF
~~~

#### Step 3 - Get the OpenShift Router exposed via MetalLB

Set up the LoadBalancer Service that routes ingress traffic to the ingress deployment.

~~~sh
cat <<"EOF" | oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig apply -f -
kind: Service
apiVersion: v1
metadata:
  annotations:
    metallb.universe.tf/address-pool: ingress-public-ip
  name: metallb-ingress
  namespace: openshift-ingress
spec:
  ports:
    - name: http
      protocol: TCP
      port: 80
      targetPort: 80
    - name: https
      protocol: TCP
      port: 443
      targetPort: 443
  selector:
    ingresscontroller.operator.openshift.io/deployment-ingresscontroller: default
  type: LoadBalancer
EOF
~~~

We already configured the wildcard record in our example DNS config:

~~~config
*.apps.example.krnl.es. IN A 192.168.122.23
~~~

So we should be able to reach the OCP Console now:

~~~sh
curl -kI https://console-openshift-console.apps.example.krnl.es

HTTP/1.1 200 OK
~~~

And if we check the clusterversion and clusteroperator we should have everything up and running now:

~~~sh
oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig get clusterversion,co

NAME                                         VERSION   AVAILABLE   PROGRESSING   SINCE   STATUS
clusterversion.config.openshift.io/version   4.11.5    True        False         3m32s   Cluster version is 4.11.5

NAME                                                                           VERSION   AVAILABLE   PROGRESSING   DEGRADED   SINCE   MESSAGE
clusteroperator.config.openshift.io/console                                    4.11.5    True        False         False      3m50s  
clusteroperator.config.openshift.io/csi-snapshot-controller                    4.11.5    True        False         False      25m  
clusteroperator.config.openshift.io/dns                                        4.11.5    True        False         False      23m  
clusteroperator.config.openshift.io/image-registry                             4.11.5    True        False         False      23m  
clusteroperator.config.openshift.io/ingress                                    4.11.5    True        False         False      53m  
clusteroperator.config.openshift.io/insights                                   4.11.5    True        False         False      25m  
clusteroperator.config.openshift.io/kube-apiserver                             4.11.5    True        False         False      54m  
clusteroperator.config.openshift.io/kube-controller-manager                    4.11.5    True        False         False      54m  
clusteroperator.config.openshift.io/kube-scheduler                             4.11.5    True        False         False      54m  
clusteroperator.config.openshift.io/kube-storage-version-migrator              4.11.5    True        False         False      25m  
clusteroperator.config.openshift.io/monitoring                                 4.11.5    True        False         False      21m  
clusteroperator.config.openshift.io/network                                    4.11.5    True        False         False      25m  
clusteroperator.config.openshift.io/openshift-apiserver                        4.11.5    True        False         False      54m  
clusteroperator.config.openshift.io/openshift-controller-manager               4.11.5    True        False         False      54m  
clusteroperator.config.openshift.io/openshift-samples                          4.11.5    True        False         False      23m  
clusteroperator.config.openshift.io/operator-lifecycle-manager                 4.11.5    True        False         False      54m  
clusteroperator.config.openshift.io/operator-lifecycle-manager-catalog         4.11.5    True        False         False      54m  
clusteroperator.config.openshift.io/operator-lifecycle-manager-packageserver   4.11.5    True        False         False      54m  
clusteroperator.config.openshift.io/service-ca                                 4.11.5    True        False         False      25m  
clusteroperator.config.openshift.io/storage                                    4.11.5    True        False         False      25m  
~~~

## Enabling Node Auto-Scaling for the Hosted Cluster

Auto-scaling can be enabled, if we choose to enable auto-scaling, when more capacity is require in our Hosted Cluster a new Agent will be installed (providing that we have spare agents). In order to enable auto-scaling we can run the following command:

> **INFO:** In this case the minimum nodes will be 2 and the maximum 5.

~~~sh
oc -n ${CLUSTERS_NAMESPACE} patch nodepool ${HOSTED_CLUSTER_NAME} --type=json -p '[{"op": "remove", "path": "/spec/replicas"},{"op":"add", "path": "/spec/autoScaling", "value": { "max": 5, "min": 2 }}]'
~~~

If 10 minutes passes without requiring the additional capacity the agent will be decommissioned and placed in the spare queue again.

1. Let's create a workload that requires a new node.

    ~~~sh
    cat <<EOF | oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig apply -f -
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      creationTimestamp: null
      labels:
        app: reversewords
      name: reversewords
      namespace: default
    spec:
      replicas: 40
      selector:
        matchLabels:
          app: reversewords
      strategy: {}
      template:
        metadata:
          creationTimestamp: null
          labels:
            app: reversewords
        spec:
          containers:
          - image: quay.io/mavazque/reversewords:latest
            name: reversewords
            resources:
              requests:
                memory: 2Gi
    status: {}
    EOF
    ~~~

2. We will see the remaining agent starts getting deployed.

    > **INFO:** The spare agent `d9198891-39f4-4930-a679-65fb142b108b` started getting provisioned.

    ~~~sh
    oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get agent -o jsonpath='{range .items[*]}BMH: {@.metadata.labels.agent-install\.openshift\.io/bmh} Agent: {@.metadata.name} State: {@.status.debugInfo.state}{"\n"}{end}'

    BMH: ocp-worker-2 Agent: 4dac1ab2-7dd5-4894-a220-6a3473b67ee6 State: added-to-existing-cluster
    BMH: ocp-worker-0 Agent: d9198891-39f4-4930-a679-65fb142b108b State: installing-in-progress
    BMH: ocp-worker-1 Agent: da503cf1-a347-44f2-875c-4960ddb04091 State: added-to-existing-cluster
    ~~~

3. If we check the nodes we will see a new one joined the cluster.

    > **INFO:** We got ocp-worker-0 added to the cluster

    ~~~sh
    oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig get nodes

    NAME           STATUS   ROLES    AGE   VERSION
    ocp-worker-0   Ready    worker   35s   v1.24.0+3882f8f
    ocp-worker-1   Ready    worker   40m   v1.24.0+3882f8f
    ocp-worker-2   Ready    worker   41m   v1.24.0+3882f8f
    ~~~

4. If we delete the workload and wait 10 minutes the node will be removed.

    ~~~sh
    oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig -n default delete deployment reversewords
    ~~~

5. After 10 minutes.

    ~~~sh
    oc --kubeconfig ${HOSTED_CLUSTER_NAME}.kubeconfig get nodes

    NAME           STATUS   ROLES    AGE   VERSION
    ocp-worker-1   Ready    worker   51m   v1.24.0+3882f8f
    ocp-worker-2   Ready    worker   52m   v1.24.0+3882f8f
    ~~~

    ~~~sh
    oc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} get agent -o jsonpath='{range .items[*]}BMH: {@.metadata.labels.agent-install\.openshift\.io/bmh} Agent: {@.metadata.name} State: {@.status.debugInfo.state}{"\n"}{end}'

    BMH: ocp-worker-2 Agent: 4dac1ab2-7dd5-4894-a220-6a3473b67ee6 State: added-to-existing-cluster
    BMH: ocp-worker-0 Agent: d9198891-39f4-4930-a679-65fb142b108b State: known-unbound
    BMH: ocp-worker-1 Agent: da503cf1-a347-44f2-875c-4960ddb04091 State: added-to-existing-cluster
    ~~~
