# Create an Agent cluster

This document explains how to create HostedClusters and NodePools using the 'Agent' platform to create bare metal worker nodes.

The Agent platform uses the [Infrastructure Operator](https://github.com/openshift/assisted-service) (AKA Assisted Installer) to add worker nodes to a hosted cluster. For a primer on the Infrastructure Operator, see [here](https://github.com/openshift/assisted-service/blob/master/docs/hive-integration/kube-api-getting-started.md).

## HyperShift flow

When you create a HostedCluster with the Agent platform, HyperShift will install the [Agent CAPI provider](https://github.com/openshift/cluster-api-provider-agent) in the HyperShift control plane namespace.

Upon scaling up a NodePool, a Machine will be created, and the CAPI provider will find a suitable Agent to match this Machine. Suitable means that the Agent is approved, is passing validations, is not currently bound (in use), and has the requirements specified on the NodePool Spec (e.g., minimum CPU/RAM, labels matching the label selector). You may monitor the installation of an Agent by checking its Status and Conditions.

Upon scaling down a NodePool, Agents will be unbound from the corresponding cluster. However, you must boot them with the Discovery Image once again before reusing them.

## HyperShift Operator requirements

* cluster-admin access to an OpenShift IPI baremetal cluster (tested with 4.9.21) to deploy the CRDs + operator (in this example, a 3 nodes compact cluster)

> **NOTE**: IPI deployment is required as it includes the `baremetal-operator` required to provision the baremetal hosts.

* 1 x filesystem type Persistent Volume to store the `etcd` database for demo purposes (3x for 'production' environments)
* 2 x filesystem type Persistent Volume to store the Assisted Service assets and database

## Components

* OCP cluster
* Local Storage Operator
* Assisted Service Operator
* Hive Operator
* HyperShift Operator

## Prerequisites: Assisted Service and Hive deployment

* Assisted Service Operator
* Hive Operator

> **NOTE**: Instead of deploying Assisted Service and Hive, RHACM can be deployed (as those are part of RHACM) but at the time of writting this document, there were some issues if using RHACM that's why only the required bits are used.

We will leverage [`tasty`](https://github.com/karmab/tasty) to deploy the required operators easily. 

* Install tasty:

~~~sh
curl -s -L https://github.com/karmab/tasty/releases/download/v0.4.0/tasty-linux-amd64 > ./tasty
sudo install -m 0755 -o root -g root ./tasty /usr/local/bin/tasty
~~~

* Install the operators:

~~~sh
tasty install assisted-service-operator hive-operator
~~~

* Wait until the operators are properly installed and the CRDs created:

~~~sh
until oc get crd/agentserviceconfigs.agent-install.openshift.io >/dev/null 2>&1 ; do sleep 1 ; done
~~~

* Create the `agentserviceconfig` object:

~~~sh
export DB_VOLUME_SIZE="10Gi"
export FS_VOLUME_SIZE="10Gi"
export OCP_VERSION="4.9"
export ARCH="x86_64"
export OCP_RELEASE_VERSION=$(curl -s https://mirror.openshift.com/pub/openshift-v4/${ARCH}/clients/ocp/latest-${OCP_VERSION}/release.txt | awk '/machine-os / { print $2 }')
export ISO_URL="https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/${OCP_VERSION}/latest/rhcos-${OCP_VERSION}.0-${ARCH}-live.${ARCH}.iso"
export ROOT_FS_URL="https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/${OCP_VERSION}/latest/rhcos-live-rootfs.${ARCH}.img"

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

* Wait for the assisted-service pod to be ready:

~~~sh
until oc wait -n assisted-installer $(oc get pods -n assisted-installer -l app=assisted-service -o name) --for condition=Ready --timeout 10s >/dev/null 2>&1 ; do sleep 1 ; done
~~~

## Prerequisites: Building the HyperShift Operator

Currently, the HyperShift operator is deployed using the `hypershift` binary, which needs to be compiled manually.
RHEL8 doesn't include go1.17 officially but it can be installed via `gvm` by following the next steps:

~~~sh
# Install prerequisites
sudo dnf install -y curl git make bison gcc glibc-devel
git clone https://github.com/openshift/hypershift.git
pushd hypershift
 
# Install gvm to install go 1.17
bash < <(curl -s -S -L https://raw.githubusercontent.com/moovweb/gvm/master/binscripts/gvm-installer)
source ${HOME}/.gvm/scripts/gvm
gvm install go1.17
gvm use go1.17

# build the binary
make hypershift
popd
~~~

Then, the `hypershift` binary can be moved to a convenient place as:

~~~sh
sudo install -m 0755 -o root -g root hypershift/bin/hypershift /usr/local/bin/hypershift
~~~

Alternatively, it can be compiled using a container as:

~~~sh
# Install prerequisites
sudo dnf install podman -y
# Compile hypershift
mkdir -p ./tmp/ && \
podman run -it -v ${PWD}/tmp:/var/tmp/hypershift-bin/:Z --rm docker.io/golang:1.17 sh -c \
  'git clone --depth 1 https://github.com/openshift/hypershift.git /var/tmp/hypershift/ && \
  cd /var/tmp/hypershift && \
  make hypershift && \
  cp bin/hypershift /var/tmp/hypershift-bin/'
sudo install -m 0755 -o root -g root ./tmp/hypershift /usr/local/bin/hypershift
~~~

> **WARNING**: At the time of writting this document, there were some issues already fixed in HyperShift but unfortunately those weren't included in the latest release of the container.

## Prerequisites: Create a custom HyperShift image

To create a custom HyperShift image, the following steps can be performed:

~~~sh
QUAY_ACCOUNT='testuser'
podman login -u ${QUAY_ACCOUNT} -p testpassword quay.io
sudo dnf install -y curl git make bison gcc glibc-devel
git clone https://github.com/openshift/hypershift.git
pushd hypershift
 
# Install gvm to install go 1.17
bash < <(curl -s -S -L https://raw.githubusercontent.com/moovweb/gvm/master/binscripts/gvm-installer)
source ${HOME}/.gvm/scripts/gvm
gvm install go1.17 -B
gvm use go1.17

# Build the binaries and the container
make build
make RUNTIME=podman IMG=quay.io/${QUAY_ACCOUNT}/hypershift:latest docker-build docker-push

sudo install -m 0755 -o root -g root hypershift/bin/hypershift /usr/local/bin/hypershift
popd
~~~

## Deploy HyperShift

Once the binary is in place, the operator deployment is performed as:

~~~sh
hypershift install
~~~

Or if using a custom image:

~~~sh
hypershift install --hypershift-image quay.io/${QUAY_ACCOUNT}/hypershift:latest
~~~

Using `hypershift install --render > hypershift-install.yaml` will create a yaml file with all the assets required to deploy HyperShift, then they can be applied as:

~~~sh
oc apply -f ./hypershift-install.yaml
~~~

## Deploy a hosted cluster

There are two main CRDs to describe a hosted cluster:
* [`HostedCluster`](https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io/v1alpha1.HostedCluster) defines the control plane hosted in the management OpenShift
* [`NodePool`](https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io/v1alpha1.NodePool) defines the nodes that will be created/attached to a hosted cluster

The `hostedcluster.spec.platform` specifies the underlying infrastructure provider for the cluster and is used to configure platform specific behavior, so depending on the environment it is required to configure it properly.

In this document we will cover the 'agent' provider.

### Requisites

* Proper DNS entries for the workers (if the worker uses `localhost` it won't work)
* DNS entry to point `api.${cluster}.${domain}` to each of the nodes where the hostedcluster will be running. This is because the hosted cluster API is exposed as a `nodeport`. For example:

~~~sh
api.hosted0.krnl.es.  IN A  192.168.124.10
api.hosted0.krnl.es.  IN A  192.168.124.11
api.hosted0.krnl.es.  IN A  192.168.124.12
~~~

* DNS entry to point `*.apps.${cluster}.${domain}` to a load balancer deployed to redirect incoming traffic to the ingresses pod [the OpenShift documentation](https://docs.openshift.com/container-platform/4.9/installing/installing_platform_agnostic/installing-platform-agnostic.html#installation-load-balancing-user-infra-example_installing-platform-agnostic) provides some instructions about this)
> **NOTE**: This is not strictly required to deploy a sample cluster but to access the exposed routes there. Also, it can be simply an A record pointing to a worker IP where the ingress pods are running and enabling the `hostedcluster.spec.infrastructureAvailabilityPolicy: SingleReplica` configuration parameter.

* Pull-secret (available at cloud.redhat.com)
* ssh public key already available (it can be created as `ssh-keygen -t rsa -f /tmp/sshkey -q -N ""`)
* A couple of baremetal hosts available to be installed

### Procedure

* Create a file containing all the variables describing the enviroment:

~~~sh
cat <<'EOF' > ./myvars
export CLUSTERS_NAMESPACE="clusters"
export HOSTED="hosted0"
export HOSTED_CLUSTER_NS="clusters-${HOSTED}"
export PULL_SECRET_NAME="${HOSTED}-pull-secret"
export MACHINE_CIDR="192.168.125.0/24"
export OCP_RELEASE_VERSION="4.9.21"
export OCP_ARCH="x86_64"
export BASEDOMAIN="krnl.es"

export PULL_SECRET_CONTENT=$(cat ~/openshift_pull.json)
export SSH_PUB=$(cat ~/.ssh/id_rsa.pub)
EOF
source ./myvars
~~~

* Create a namespace to host the HostedCluster and secrets

~~~sh
envsubst <<"EOF" | oc apply -f -
apiVersion: v1
kind: Namespace
metadata:
 name: ${CLUSTERS_NAMESPACE}
EOF

export PS64=$(echo -n ${PULL_SECRET_CONTENT} | base64 -w0)
envsubst <<"EOF" | oc apply -f -
apiVersion: v1
data:
 .dockerconfigjson: ${PS64}
kind: Secret
metadata:
 name: ${PULL_SECRET_NAME}
 namespace: ${CLUSTERS_NAMESPACE}
type: kubernetes.io/dockerconfigjson
EOF
 
envsubst <<"EOF" | oc apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: ${HOSTED}-ssh-key
  namespace: ${CLUSTERS_NAMESPACE}
stringData:
  id_rsa.pub: ${SSH_PUB}
EOF
~~~

* Create the `hostedcluster` and the `nodepool` objects:

~~~sh
envsubst <<"EOF" | oc apply -f -
apiVersion: hypershift.openshift.io/v1alpha1
kind: HostedCluster
metadata:
  name: ${HOSTED}
  namespace: ${CLUSTERS_NAMESPACE}
spec:
  release:
    image: "quay.io/openshift-release-dev/ocp-release:${OCP_RELEASE_VERSION}-${OCP_ARCH}"
  pullSecret:
    name: ${PULL_SECRET_NAME}
  sshKey:
    name: "${HOSTED}-ssh-key"
  networking:
    serviceCIDR: "172.31.0.0/16"
    podCIDR: "10.132.0.0/14"
    machineCIDR: "${MACHINE_CIDR}"
  platform:
    agent:
      agentNamespace: ${HOSTED_CLUSTER_NS}
    type: Agent
  infraID: ${HOSTED}
  dns:
    baseDomain: ${BASEDOMAIN}
  services:
  - service: APIServer
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: NodePort
  - service: OAuthServer
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: NodePort
  - service: OIDC
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: None
  - service: Konnectivity
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: NodePort
  - service: Ignition
    servicePublishingStrategy:
      nodePort:
        address: api.${HOSTED}.${BASEDOMAIN}
      type: NodePort
EOF

envsubst <<"EOF" | oc apply -f -
apiVersion: hypershift.openshift.io/v1alpha1
kind: NodePool
metadata:
  name: ${HOSTED}-workers
  namespace: ${CLUSTERS_NAMESPACE}
spec:
  clusterName: ${HOSTED}
  nodeCount: 0
  management:
    autoRepair: false
    upgradeType: Replace
  platform:
    type: Agent
  release:
    image: "quay.io/openshift-release-dev/ocp-release:${OCP_RELEASE_VERSION}-${OCP_ARCH}"
EOF
~~~

> **NOTE**: The `HostedCluster` and `NodePool` objects can be created using the `hypershift` binary as `hypershift create cluster`. See the `hypershift create cluster -h` output for more information.

After a while, a number of pods will be created in the `${CLUSTERS_NAMESPACE}` namespace. Those pods are the control plane of the hosted cluster.

~~~sh
oc get pods -n ${HOSTED_CLUSTER_NS}

NAME                                              READY   STATUS    RESTARTS   AGE
capi-provider-79789f59b-2pmqr                     1/1     Running   0          4h4m
catalog-operator-68fcc67c9-8pkzk                  2/2     Running   0          4h3m
certified-operators-catalog-78ff684f4f-kt72n      1/1     Running   0          4h3m
cluster-api-655c8ff4fb-2n2mx                      1/1     Running   0          4h4m
cluster-autoscaler-86d9474fcf-4wlcj               1/1     Running   0          4h3m
cluster-policy-controller-684f8fcdcf-fs5q8        1/1     Running   0          4h4m
cluster-version-operator-9675499d4-n9w6k          2/2     Running   0          4h4m
community-operators-catalog-64ff7fb96c-68lpx      1/1     Running   0          4h3m
control-plane-operator-657f5d6864-qv667           1/1     Running   0          4h4m
etcd-0                                            1/1     Running   0          4h4m
hosted-cluster-config-operator-5f7479c8db-4vb4m   1/1     Running   0          4h3m
ignition-server-7797c5f7-2cf7k                    1/1     Running   0          4h4m
ingress-operator-85554f497d-kxj6t                 2/2     Running   0          4h3m
konnectivity-agent-85df5767cb-cg58g               1/1     Running   0          4h4m
konnectivity-server-67fdfdcd5b-lnm5c              1/1     Running   0          4h4m
kube-apiserver-7756ddcc98-gxmvl                   2/2     Running   0          4h4m
kube-controller-manager-6dc6475c58-tnt8q          1/1     Running   0          36m
kube-scheduler-6f6585f8df-7ztjv                   1/1     Running   0          4h4m
machine-approver-c8c68ffb9-ndzr5                  1/1     Running   0          4h3m
oauth-openshift-78cf87c877-l2smf                  1/1     Running   0          4h1m
olm-operator-68c8c57787-8czbz                     2/2     Running   0          4h3m
openshift-apiserver-7b69cb4f5-mw9pp               2/2     Running   0          4h4m
openshift-controller-manager-8d748b754-jp9vr      1/1     Running   0          4h4m
openshift-oauth-apiserver-755d67dcdd-tlb5f        1/1     Running   0          4h4m
packageserver-8645d77646-cvd8m                    2/2     Running   0          4h3m
redhat-marketplace-catalog-79cdb745d7-cx7lb       1/1     Running   0          4h3m
redhat-operators-catalog-69fcdfc876-l4c8j         1/1     Running   0          4h3m
~~~

> **NOTE**: Using `agent` also deploys the capi-provider pod that manages the agents.

The hosted cluster's kubeconfig can be extracted as:

~~~sh
oc extract -n ${CLUSTERS_NAMESPACE} secret/${HOSTED}-admin-kubeconfig --to=- > ${HOSTED}-kubeconfig
oc get clusterversion --kubeconfig=${HOSTED}-kubeconfig
~~~

### Adding a bare metal worker

We will leverage the Assisted Service and Hive to create the custom ISO as well as the Baremetal Operator to perform the installation.

* Enable the Baremetal Operator to watch all namespaces as the `baremetalhost` object for the hosted cluster will be created in the `${HOSTED_CLUSTER_NS}` namespace:

~~~sh
oc patch provisioning provisioning-configuration --type merge -p '{"spec":{"watchAllNamespaces": true }}'
~~~

> **NOTE**: This will trigger a restart of the `metal3` pod in the `openshift-machine-api` namespace.

* Wait until the `metal3` pod is ready again:

~~~sh
until oc wait -n openshift-machine-api $(oc get pods -n openshift-machine-api -l baremetal.openshift.io/cluster-baremetal-operator=metal3-state -o name) --for condition=containersready --timeout 10s >/dev/null 2>&1 ; do sleep 1 ; done
~~~

* Create the pull secret in the `${HOSTED_CLUSTER_NS}` namespace as it needs to be injected later in the `InfraEnv`

~~~sh
export PS64=$(echo -n ${PULL_SECRET_CONTENT} | base64 -w0)
envsubst <<"EOF" | oc apply -f -
apiVersion: v1
data:
 .dockerconfigjson: ${PS64}
kind: Secret
metadata:
 name: ${PULL_SECRET_NAME}
 namespace: ${HOSTED_CLUSTER_NS}
type: kubernetes.io/dockerconfigjson
EOF
~~~

* Create a custom `InfraEnv` with just the pull secret, ssh key and optionally, the NTP

~~~sh
envsubst <<"EOF" | oc apply -f -
apiVersion: agent-install.openshift.io/v1beta1
kind: InfraEnv
metadata:
  name: ${HOSTED}
  namespace: ${HOSTED_CLUSTER_NS}
spec:
  additionalNTPSources:
    - "clock.corp.redhat.com"
  pullSecretRef:
    name: ${PULL_SECRET_NAME}
  sshAuthorizedKey: ${SSH_PUB}
EOF
~~~

> **NOTE**: If you wish to use an `InfraEnv` (and its associated `Agents`) from a namespace that isn't the ${HOSTED_CLUSTER_NS}, you must create a role for capi-provider-agent in that namespace (this is the same namespace as specified in the hosted cluster Spec (`spec.platform.agent.agentNamespace`).
~~~sh
envsubst <<"EOF" | oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  creationTimestamp: null
  name: capi-provider-role
  namespace: ${INFRA_ENV_NS}
rules:
- apiGroups:
  - agent-install.openshift.io
  resources:
  - agents
  verbs:
  - '*'
EOF
~~~

This will generate a custom ISO for the host to be installed.

~~~sh
until oc wait -n "${HOSTED_CLUSTER_NS}" $(oc get infraenv "${HOSTED}" -n "${HOSTED_CLUSTER_NS}" -o name) --for condition=ImageCreated --timeout 10s >/dev/null 2>&1 ; do sleep 1 ; done
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
  namespace: ${HOSTED_CLUSTER_NS}
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
  namespace: ${HOSTED_CLUSTER_NS}
  labels:
    infraenvs.agent-install.openshift.io: ${HOSTED}
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

> **NOTE**: The label references the infraenv previously created and inspection is disabled as it would be done by the Assisted Service instead.

After a few minutes, an `agent` object is created (the name is the hardware UUID, in our case, we created it upfront):

~~~sh
until oc get agent -n ${HOSTED_CLUSTER_NS} ${UUID} >/dev/null 2>&1 ; do sleep 1 ; done
export AGENT=$(oc get agent -n ${HOSTED_CLUSTER_NS} ${UUID} -o name)
~~~

* The `agent` needs to be approved and configured for the installation:

~~~sh
oc patch ${AGENT} -n ${HOSTED_CLUSTER_NS} -p '{"spec":{"installation_disk_id":"/dev/sda","approved":true,"hostname":"ocp-worker-0.hosted0.krnl.es","role":"worker"}}' --type merge
~~~

* Finally, scaling up the nodepool will trigger the installation:

~~~sh
oc patch nodepool/${HOSTED}-workers -n ${CLUSTERS_NAMESPACE} -p '{"spec":{"nodeCount": 1}}' --type merge
~~~

* Then the worker is added to the cluster:

~~~sh
oc get nodes --kubeconfig=${HOSTED}-kubeconfig
NAME                           STATUS   ROLES    AGE   VERSION
ocp-worker-0.hosted0.krnl.es   Ready    worker   63m   v1.22.3+fdba464

oc get co --kubeconfig=${HOSTED}-kubeconfig
NAME                                       VERSION   AVAILABLE   PROGRESSING   DEGRADED   SINCE   MESSAGE
console                                    4.9.21    False       False         False      62m     RouteHealthAvailable: failed to GET route (https://console-openshift-console.apps.hosted0.krnl.es): Get "https://console-openshift-console.apps.hosted0.krnl.es": x509: certificate is valid for *.apps.ocp.krnl.es, not console-openshift-console.apps.hosted0.krnl.es
csi-snapshot-controller                    4.9.21    True        False         False      61m     
dns                                        4.9.21    True        False         False      61m     
image-registry                             4.9.21    True        False         False      61m     
ingress                                    4.9.21    True        False         True       51m     The "default" ingress controller reports Degraded=True: DegradedConditions: One or more other status conditions indicate a degraded state: PodsScheduled=False (PodsNotScheduled: Some pods are not scheduled: Pod "router-default-7f9dc784cb-mlg9m" cannot be scheduled: 0/1 nodes are available: 1 node(s) didn't have free ports for the requested pod ports. Make sure you have sufficient worker nodes.), CanaryChecksSucceeding=False (CanaryChecksRepetitiveFailures: Canary route checks for the default ingress controller are failing)
kube-apiserver                             4.9.21    True        False         False      4h27m   
kube-controller-manager                    4.9.21    True        False         False      4h27m   
kube-scheduler                             4.9.21    True        False         False      4h27m   
kube-storage-version-migrator              4.9.21    True        False         False      61m     
monitoring                                           False       True          True       45m     Rollout of the monitoring stack failed and is degraded. Please investigate the degraded status error.
network                                    4.9.21    True        False         False      63m     
node-tuning                                4.9.21    True        False         False      61m     
openshift-apiserver                        4.9.21    True        False         False      4h27m   
openshift-controller-manager               4.9.21    True        False         False      4h27m   
openshift-samples                          4.9.21    True        False         False      60m     
operator-lifecycle-manager                 4.9.21    True        False         False      4h26m   
operator-lifecycle-manager-catalog         4.9.21    True        False         False      4h26m   
operator-lifecycle-manager-packageserver   4.9.21    True        False         False      4h27m   
service-ca                                 4.9.21    True        False         False      62m     
storage                                    4.9.21    True        False         False      62m    

oc get clusterversion --kubeconfig=${HOSTED}-kubeconfig
NAME      VERSION   AVAILABLE   PROGRESSING   SINCE   STATUS
version             False       True          4h28m   Unable to apply 4.9.21: some cluster operators have not yet rolled out
~~~

> **NOTE**: Some cluster operators are degraded because there is only a single worker and they require at least 2. However setting the `hostedcluster.spec.infrastructureAvailabilityPolicy: SingleReplica` configuration parameter disables the requirement and will make the clusters operator available with a single worker.

* After adding another worker, the hosted cluster is completely available as well as all the cluster operators:

~~~sh
export BMC_USERNAME=$(echo -n "root" | base64 -w0)
export BMC_PASSWORD=$(echo -n "calvin" | base64 -w0)
# In our case, we are using the installer VM as the sushy-tools server
# export BMC_IP=$(hostname -i)
export BMC_IP="192.168.124.228"
export WORKER_NAME="ocp-worker-1"
export BOOT_MAC_ADDRESS="aa:bb:cc:dd:ee:fa"
export UUID=11111111-1111-1111-1111-111111111112
export REDFISH="redfish-virtualmedia+http://${BMC_IP}:8000/redfish/v1/Systems/${UUID}"

envsubst <<"EOF" | oc apply -f -
apiVersion: v1
data:
  password: ${BMC_PASSWORD}
  username: ${BMC_USERNAME}
kind: Secret
metadata:
  name: ${WORKER_NAME}-bmc-secret
  namespace: ${HOSTED_CLUSTER_NS}
type: Opaque
EOF

envsubst <<"EOF" | oc apply -f -
apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
  name: ${WORKER_NAME}
  namespace: ${HOSTED_CLUSTER_NS}
  labels:
    infraenvs.agent-install.openshift.io: ${HOSTED}
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

until oc get agent -n ${HOSTED_CLUSTER_NS} ${UUID} >/dev/null 2>&1 ; do sleep 1 ; done
export AGENT=$(oc get agent -n ${HOSTED_CLUSTER_NS} ${UUID} -o name)

oc patch ${AGENT} -n ${HOSTED_CLUSTER_NS} -p '{"spec":{"installation_disk_id":"/dev/sda","approved":true,"hostname":"ocp-worker-1.hosted0.krnl.es","role":"worker"}}' --type merge

oc patch nodepool/${HOSTED}-workers -n ${CLUSTERS_NAMESPACE} -p '{"spec":{"nodeCount": 2}}' --type merge
~~~

~~~sh
oc get nodes --kubeconfig=hosted0-kubeconfig  
NAME                           STATUS   ROLES    AGE    VERSION
ocp-worker-0.hosted0.krnl.es   Ready    worker   19h    v1.22.3+fdba464
ocp-worker-1.hosted0.krnl.es   Ready    worker   2m2s   v1.22.3+fdba464

oc get hostedcluster -n clusters hosted0
NAME      VERSION   KUBECONFIG                 PROGRESS    AVAILABLE   REASON
hosted0   4.9.21    hosted0-admin-kubeconfig   Completed   True        HostedClusterAsExpected

oc get co --kubeconfig=hosted0-kubeconfig  
NAME                                       VERSION   AVAILABLE   PROGRESSING   DEGRADED   SINCE   MESSAGE
console                                    4.9.21    True        False         False      68s     
csi-snapshot-controller                    4.9.21    True        False         False      19h     
dns                                        4.9.21    True        False         False      19h     
image-registry                             4.9.21    True        False         False      19h     
ingress                                    4.9.21    True        False         False      19h     
kube-apiserver                             4.9.21    True        False         False      22h     
kube-controller-manager                    4.9.21    True        False         False      22h     
kube-scheduler                             4.9.21    True        False         False      22h     
kube-storage-version-migrator              4.9.21    True        False         False      19h     
monitoring                                 4.9.21    True        False         False      6m26s   
network                                    4.9.21    True        False         False      19h     
node-tuning                                4.9.21    True        False         False      19h     
openshift-apiserver                        4.9.21    True        False         False      22h     
openshift-controller-manager               4.9.21    True        False         False      22h     
openshift-samples                          4.9.21    True        False         False      19h     
operator-lifecycle-manager                 4.9.21    True        False         False      22h     
operator-lifecycle-manager-catalog         4.9.21    True        False         False      22h     
operator-lifecycle-manager-packageserver   4.9.21    True        False         False      22h     
service-ca                                 4.9.21    True        False         False      19h     
storage                                    4.9.21    True        False         False      19h

oc get clusterversion --kubeconfig=hosted0-kubeconfig  
NAME      VERSION   AVAILABLE   PROGRESSING   SINCE   STATUS
version   4.9.21    True        False         94s     Cluster version is 4.9.21
~~~