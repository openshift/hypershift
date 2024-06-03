# Create Heterogeneous NodePools on Agent HostedClusters

This document explains how to create heterogeneous nodepools on agent platform. 
Please [refer](create-agent-cluster.md) to set up the env for agent cluster, this document only covers the things you need to configure to have heterogeneous nodepools.

## Configure AgentServiceConfig with two heterogeneous architecture OS images

~~~sh
export DB_VOLUME_SIZE="10Gi"
export FS_VOLUME_SIZE="10Gi"
export OCP_VERSION="4.15.0"
export OCP_MAJMIN=${OCP_VERSION%.*}

export ARCH_X86="x86_64"
export OCP_RELEASE_VERSION_X86=$(curl -s https://mirror.openshift.com/pub/openshift-v4/${ARCH_X86}/clients/ocp/${OCP_VERSION}/release.txt | awk '/machine-os / { print $2 }')
export ISO_URL_X86="https://mirror.openshift.com/pub/openshift-v4/${ARCH_X86}/dependencies/rhcos/${OCP_MAJMIN}/${OCP_VERSION}/rhcos-${OCP_VERSION}-${ARCH_X86}-live.${ARCH_X86}.iso"
export ROOT_FS_URL_X8="https://mirror.openshift.com/pub/openshift-v4/${ARCH_X86}/dependencies/rhcos/${OCP_MAJMIN}/${OCP_VERSION}/rhcos-${OCP_VERSION}-${ARCH_X86}-live-rootfs.${ARCH_X86}.img"

export ARCH_PPC64LE="ppc64le"
export OCP_RELEASE_VERSION_PPC64LE=$(curl -s https://mirror.openshift.com/pub/openshift-v4/${ARCH_PPC64LE}/clients/ocp/${OCP_VERSION}/release.txt | awk '/machine-os / { print $2 }')
export ISO_URL_PPC64LE="https://mirror.openshift.com/pub/openshift-v4/${ARCH_PPC64LE}/dependencies/rhcos/${OCP_MAJMIN}/${OCP_VERSION}/rhcos-${OCP_VERSION}-${ARCH_PPC64LE}-live.${ARCH_PPC64LE}.iso"
export ROOT_FS_URL_PPC64LE="https://mirror.openshift.com/pub/openshift-v4/${ARCH_PPC64LE}/dependencies/rhcos/${OCP_MAJMIN}/${OCP_VERSION}/rhcos-${OCP_VERSION}-${ARCH_PPC64LE}-live-rootfs.${ARCH_PPC64LE}.img"

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
      version: "${OCP_RELEASE_VERSION_X86}"
      url: "${ISO_URL_X86}"
      rootFSUrl: "${ROOT_FS_URL_X8}"
      cpuArchitecture: "${ARCH_X86}"
    - openshiftVersion: "${OCP_VERSION}"
      version: "${OCP_RELEASE_VERSION_PPC64LE}"
      url: "${ISO_URL_PPC64LE}"
      rootFSUrl: "${ROOT_FS_URL_PPC64LE}"
      cpuArchitecture: "${ARCH_PPC64LE}"
EOF
~~~

The above configuration allows you to create ISO for both x86_64 and ppc64le architectures.

## Configure DNS

`*.apps.<cluster_name>` record can be pointed to either one of the worker node where ingress application is hosted, or if you are able to set up a load balancer on top of the worker nodes, point this record to this load balancer.
When you are creating heterogeneous nodepool, please make sure the workers are reachable from each other or keep them in the same network.

## Create a Hosted Cluster

Need to use multi arch release image while creating the cluster to use heterogeneous nodepools. Find the latest multi arch images from [here](https://multi.ocp.releases.ci.openshift.org) 
~~~sh
export CLUSTERS_NAMESPACE="clusters"
export HOSTED_CLUSTER_NAME="example"
export HOSTED_CONTROL_PLANE_NAMESPACE="${CLUSTERS_NAMESPACE}-${HOSTED_CLUSTER_NAME}"
export BASEDOMAIN="krnl.es"
export PULL_SECRET_FILE=$PWD/pull-secret
export OCP_RELEASE=4.15.0-multi
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

## Create Heterogeneous NodePool

By default, the previous section creates a nodepool on `x86_64` architecture, to create heterogeneous nodepools, you can apply the below manifest with the architecture you want.

~~~shell
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: ${HOSTED_CLUSTER_NAME}
  namespace: ${CLUSTERS_NAMESPACE}
spec:
  arch: ${ARCH_PPC64LE}
  clusterName: ${HOSTED_CLUSTER_NAME}
  management:
    autoRepair: false
    upgradeType: InPlace
  nodeDrainTimeout: 0s
  nodeVolumeDetachTimeout: 0s
  platform:
    agent:
      agentLabelSelector:
        matchLabels:
          inventory.agent-install.openshift.io/cpu-architecture: ${ARCH_PPC64LE}
    type: Agent
  release:
    image: quay.io/openshift-release-dev/ocp-release:${OCP_RELEASE}
  replicas: 0
~~~
This will create nodepool of architecture `ppc64le` with 0 replicas.

~~~shell
      agentLabelSelector:
        matchLabels:
          inventory.agent-install.openshift.io/cpu-architecture: ppc64le
~~~
This selector block is used to select the agents which are matching given label. Which will ensure it will select only the agents from `ppc64le` arch when it scaled.

## Create Infraenv

For heterogeneous nodepools, you need to create infraenv for each architecture you are going to have in your HCP.
i.e. for heterogeneous nodepools with x86_64 and ppc64le architecture, you will need to create two infraenvs with both architectures.

~~~sh
export SSH_PUB_KEY=$(cat $HOME/.ssh/id_rsa.pub)

envsubst <<"EOF" | oc apply -f -
apiVersion: agent-install.openshift.io/v1beta1
kind: InfraEnv
metadata:
  name: ${HOSTED_CLUSTER_NAME}-${ARCH_X86}
  namespace: ${HOSTED_CONTROL_PLANE_NAMESPACE}
spec:
  cpuArchitecture: ${ARCH_X86}
  pullSecretRef:
    name: pull-secret
  sshAuthorizedKey: ${SSH_PUB_KEY}
EOF

envsubst <<"EOF" | oc apply -f -
apiVersion: agent-install.openshift.io/v1beta1
kind: InfraEnv
metadata:
  name: ${HOSTED_CLUSTER_NAME}-${ARCH_PPC64LE}
  namespace: ${HOSTED_CONTROL_PLANE_NAMESPACE}
spec:
  cpuArchitecture: ${ARCH_PPC64LE}
  pullSecretRef:
    name: pull-secret
  sshAuthorizedKey: ${SSH_PUB_KEY}
EOF
~~~
This will create two infraenvs with x86_64 & ppc64le architectures. Before creating this, need to ensure respective architecture's OS image is added in `AgentServiceConfig`

After this you can use the above infraenvs to get the minimal iso and follow the [create-agent-cluster.md](create-agent-cluster.md) to add them to your cluster as agents.

## Scale the NodePool

Once your agents are approved, you can scale the nodepools. `agentLabelSelector` configured in nodepool ensures that only matching agents gets added to the cluster.
This also aids in descaling the nodepool. To remove specific arch nodes from the cluster, you can descale the corresponding nodepool.