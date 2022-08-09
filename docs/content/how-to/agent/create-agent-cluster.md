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
Agent by checking its Status and Conditions.

Upon scaling down a NodePool, Agents will be unbound from the corresponding cluster. However, you must boot them with the Discovery
Image once again before reusing them.

## Install Hypershift Operator

Follow instructions for building the hypershift CLI in [Getting
Started](https://hypershift-docs.netlify.app/getting-started/#prerequisites)

Install the Hypershift Operator
~~~sh
hypershift install
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
export OCP_VERSION="4.10.16"
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

This can be as simple as a A record pointing to one of the nodes in the management cluster (i.e. the cluster running the HCP).  It can also point to a [load balancer](https://docs.openshift.com/container-platform/4.9/installing/installing_platform_agnostic/installing-platform-agnostic.html#installation-load-balancing-user-infra-example_installing-platform-agnostic) deployed to redirect incoming traffic to the ingress pods.

## Create a Hosted Cluster

~~~sh
export CLUSTERS_NAMESPACE="clusters"
export HOSTED_CLUSTER_NAME="example"
export HOSTED_CONTROL_PLANE_NAMESPACE="${CLUSTERS_NAMESPACE}-${HOSTED_CLUSTER_NAME}"
export BASEDOMAIN="krnl.es"
export PULL_SECRET_FILE=$PWD/pull-secret

# Typically the namespace is created by the hypershift-operator 
# but agent cluster creation generates a capi-provider role that
# needs the namespace to already exist
oc create ns ${HOSTED_CONTROL_PLANE_NAMESPACE}
bin/hypershift create cluster agent --name=${HOSTED_CLUSTER_NAME} --pull-secret=${PULL_SECRET_FILE} --agent-namespace=${HOSTED_CONTROL_PLANE_NAMESPACE} --api-server-address=api.${HOSTED_CLUSTER_NAME}.${BASEDOMAIN}
~~~

## Create an InfraEnv

An InfraEnv is a enviroment to which hosts booting the live ISO can join as Agents.  In this case, the Agents will be created in the
same namespace as our HCP

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

This will generate a live ISO that allows machines (VMs or bare meatal) to join as Agents

~~~sh
oc get InfraEnv ${HOSTED_CLUSTER_NAME} -ojsonpath="{.status.isoDownloadURL}"
~~~

## Adding Agents

You can add Agents by manually configuring the machine to boot with the live ISO or by using metal3

### Manual

The live ISO may be downloaded and used to boot a node (bare metal or VM).  On boot, the node will communicate with the
assisted-service and register as an Agent in the the same namespace as the InfraEnv.

Once each Agent is created, optionally set its installation_disk_id and hostname in the Spec. Then approve it to
indicate that the Agent is ready for use.

~~~sh
$ oc get agents -n ${HOSTED_CONTROL_PLANE_NAMESPACE}
NAME                                   CLUSTER   APPROVED   ROLE          STAGE
86f7ac75-4fc4-4b36-8130-40fa12602218                        auto-assign
e57a637f-745b-496e-971d-1abbf03341ba                        auto-assign

$ oc patch agent 86f7ac75-4fc4-4b36-8130-40fa12602218 -p '{"spec":{"installation_disk_id":"/dev/sda","approved":true,"hostname":"worker-0.example.krnl.es"}}' --type merge
$ oc patch agent 23d0c614-2caa-43f5-b7d3-0b3564688baa -p '{"spec":{"installation_disk_id":"/dev/sda","approved":true,"hostname":"worker-1.example.krnl.es"}}' --type merge

$ oc get agents -n ${HOSTED_CONTROL_PLANE_NAMESPACE}
NAME                                   CLUSTER   APPROVED   ROLE          STAGE
86f7ac75-4fc4-4b36-8130-40fa12602218             true       auto-assign
e57a637f-745b-496e-971d-1abbf03341ba             true       auto-assign
~~~

### metal3

We will leverage the Assisted Service and Hive to create the custom ISO as well as the Baremetal Operator to perform the
installation.

* Enable the Baremetal Operator to watch all namespaces as the `baremetalhost` object for the hosted cluster will be created in the
  `${HOSTED_CONTROL_PLANE_NAMESPACE}` namespace:

~~~sh
oc patch provisioning provisioning-configuration --type merge -p '{"spec":{"watchAllNamespaces": true }}'
~~~

> **NOTE**: This will trigger a restart of the `metal3` pod in the `openshift-machine-api` namespace.

* Wait until the `metal3` pod is ready again:

~~~sh
until oc wait -n openshift-machine-api $(oc get pods -n openshift-machine-api -l baremetal.openshift.io/cluster-baremetal-operator=metal3-state -o name) --for condition=containersready --timeout 10s >/dev/null 2>&1 ; do sleep 1 ; done
~~~

* Set the variables required for the BMC details of the worker that is going to be added:

~~~sh
export BMC_USERNAME=$(echo -n "root" | base64 -w0)
export BMC_PASSWORD=$(echo -n "calvin" | base64 -w0)
export BMC_IP="192.168.124.228"
export WORKER_NAME="ocp-worker-0"
export BOOT_MAC_ADDRESS="aa:bb:cc:dd:ee:ff"
export UUID=11111111-1111-1111-1111-111111111111
export REDFISH="redfish-virtualmedia+http://${BMC_IP}:8000/redfish/v1/Systems/${UUID}"
~~~

* Create the BMC secret to host the BMC user and password:

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

* Create the BMH object:

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

* If you wish to indicate an installation disk, use the [rootDeviceHints](https://github.com/metal3-io/baremetal-operator/blob/main/docs/api.md#rootdevicehints) in the BMH Spec.

* If you wish to manually set a hostname, set it via an annotation on the BMH: bmac.agent-install.openshift.io/hostname

* Agent CRs that are created via BMH will automatically be approved.

## Scale the NodePool

Scale the NodePool to two nodes

~~~sh
oc scale NodePool -n ${CLUSTERS_NAMESPACE} ${HOSTED_CLUSTER_NAME} --replicas=2
~~~

Verify the Agents are assigned to the hosted cluster

~~~sh
$ oc get agents
NAME                                   CLUSTER   APPROVED   ROLE     STAGE
86f7ac75-4fc4-4b36-8130-40fa12602218   example   true       worker   Done
e57a637f-745b-496e-971d-1abbf03341ba   example   true       worker   Done
~~~

Verify machines joined the hosted cluster as Nodes

~~~sh
$ oc get machines
NAME                      CLUSTER         NODENAME                   PROVIDERID                                     PHASE     AGE     VERSION
example-bcc6c5c95-f6shj   example-4z9kg   worker-0.example.krnl.es   agent://e57a637f-745b-496e-971d-1abbf03341ba   Running   3h21m   4.10.18
example-bcc6c5c95-jskr8   example-4z9kg   worker-1.example.krnl.es   agent://86f7ac75-4fc4-4b36-8130-40fa12602218   Running   3h21m   4.10.18

$ hypershift create kubeconfig > kubeconfig
$ export KUBECONFIG=$PWD/kubeconfig

$ oc get nodes
NAME                       STATUS   ROLES    AGE     VERSION
worker-0.example.krnl.es   Ready    worker   3h31m   v1.23.5+3afdacb
worker-1.example.krnl.es   Ready    worker   3h31m   v1.23.5+3afdacb

$ oc get clusterversion
NAME      VERSION   AVAILABLE   PROGRESSING   SINCE   STATUS
version   4.10.18   True        False         16s     Cluster version is 4.10.18
~~~

## Handling Ingress

Every OpenShift cluster comes set up with a default application ingress
controller, which is expected have an external DNS record associated with it.

For example, if a HyperShift cluster named `example` with the base domain
`krnl.es` is created, then the wildcard domain
`*.apps.example.krnl.es` is expected to be routable.

### Set up a LoadBalancer and wildcard DNS record for the `*.apps`.

This option requires deploying MetalLB, configuring a new LoadBalancer service that routes to the ingress deployment, as well as assigning a wildcard DNS entry to the LoadBalancer's IP address.

**Step 1**

Set up [MetalLB](https://docs.openshift.com/container-platform/4.10/networking/metallb/about-metallb.html) so that when you create a service of type LoadBalancer, MetalLB will add an external IP address for the service.
~~~sh
hypershift create kubeconfig > kubeconfig
export KUBECONFIG=$PWD/kubeconfig

cat <<"EOF" | oc apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: metallb
  labels:
    openshift.io/cluster-monitoring: "true"
  annotations:
    workload.openshift.io/allowed: management
EOF

cat <<"EOF" | oc apply -f -
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: metallb-operator-operatorgroup
  namespace: metallb
spec:
  targetNamespaces:
  - metallb
EOF

cat <<"EOF" | oc apply -f -
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
EOF

cat <<"EOF" | oc apply -f -
apiVersion: metallb.io/v1beta1
kind: MetalLB
metadata:
  name: metallb
  namespace: metallb
EOF
~~~

**Step 2**

Create an AddressPool with a single IP address.\
**_Note:_** The IP address assigned to the service must be on the same subnet as the network used by the cluster nodes.\
Change the INGRESS_IP variable to fit your environment.
~~~sh
export INGRESS_IP=192.168.127.77

envsubst <<"EOF" | oc apply -f -
apiVersion: metallb.io/v1alpha1
kind: AddressPool
metadata:
  name: ingress-public-ip
  namespace: metallb
spec:
  protocol: layer2
  autoAssign: false
  addresses:
    - ${INGRESS_IP}-${INGRESS_IP}
EOF
~~~

**Step 3**

Set up the LoadBalancer Service that routes ingress traffic to the ingress deployment.
~~~sh
cat <<"EOF" | oc apply -f -
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

**Step 4**

Configure wildcard DNS A record or CNAME that references the LoadBalancer Service's external IP.
Configure a wildcard *.apps.<cluster_name>.<base_domain>. DNS entry referencing the IP stored in
$INGRESS_IP that is routable both internally and externally to the cluster.
No newline at end of file