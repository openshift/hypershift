---
title: Getting Started with Agent
description: Deploy your first HyperShift hosted cluster on bare metal and edge infrastructure
---

# Getting Started with HyperShift on Agent

The Agent platform provides the most flexible deployment option for HyperShift, supporting bare metal infrastructure, edge deployments, and air-gapped environments. It uses the Infrastructure Operator (Assisted Installer) to manage worker nodes.

## Choose Your Deployment Model

=== "Agent (Self-Managed Only)"

    **Bare Metal & Edge Infrastructure with HyperShift**

    - ✅ **Bare Metal** - Direct deployment on physical hardware
    - ✅ **Edge Computing** - Distributed locations and edge deployments
    - ✅ **Air-Gapped** - Disconnected environments without internet access
    - ✅ **Flexible Infrastructure** - Support for heterogeneous hardware
    - 🛠️ **Self-Managed** - You control all infrastructure components
    - ⚠️ **Complex Setup** - Requires manual hardware and network management

    Perfect for: On-premises deployments, edge computing, air-gapped environments, custom hardware configurations

!!! note "Single Deployment Option"

    The Agent platform only supports self-managed HyperShift deployments. You provide and manage the physical infrastructure, networking, and DNS configuration.

## Agent Platform Quickstart

### Prerequisites

Before you begin, ensure you have:

- [x] **OpenShift management cluster** (4.14+) with admin access
- [x] **Bare metal hosts** or VMs for worker nodes
- [x] **DNS infrastructure** configured for hosted cluster domains
- [x] **Network connectivity** between management cluster and worker hosts
- [x] **DHCP or static IP** configuration for worker nodes
- [x] **BMC access** (for bare metal hosts) with Redfish/IPMI
- [x] **Pull secret** from [Red Hat Console](https://console.redhat.com/openshift/install/platform-agnostic/user-provisioned)

### Step 1: Install HyperShift CLI

!!! important "CLI Tool Support"

    - **`hcp` CLI**: Officially supported for production use and cluster management
    - **`hypershift` CLI**: Developer-only tool, not supported for production. Used primarily for operator installation and development workflows

Choose one of these methods:

=== "From MCE Console (Recommended)"

    For self-managed deployments with MCE/ACM:

    ```bash
    # Access your MCE/ACM console and navigate to:
    # Infrastructure > Clusters > Create cluster > Hosted control plane
    # The console provides download links for the HCP CLI

    # Follow the download instructions provided in the console
    ```

=== "Build from Source"

    **Prerequisites:**
    - Go 1.24+ installed
    - Git installed
    - Make installed

    ```bash
    # Clone the repository
    git clone https://github.com/openshift/hypershift.git
    cd hypershift

    # Build hypershift CLI
    make hypershift

    # Install the CLI tool
    sudo install -m 0755 bin/hypershift /usr/local/bin/hypershift

    # Verify installation
    hypershift version
    ```

=== "Extract from Operator Image"

    ```bash
    # Extract from specific release
    export HYPERSHIFT_RELEASE=4.15

    podman cp $(podman create --name hypershift --rm --pull always \
      quay.io/hypershift/hypershift-operator:${HYPERSHIFT_RELEASE}):/usr/bin/hypershift \
      /tmp/hypershift && podman rm -f hypershift

    sudo install -m 0755 -o root -g root /tmp/hypershift /usr/local/bin/hypershift
    ```

### Step 2: Install Required Operators

Install the HyperShift operator and dependencies:

```bash
# Install HyperShift operator (specify version to match your OCP version)
hypershift install --hypershift-image quay.io/hypershift/hypershift-operator:4.15

# Verify installation
oc get pods -n hypershift
```

!!! note "RHACM Integration"

    If Red Hat Advanced Cluster Management (RHACM) is already installed, you can skip the Assisted Service and Hive operator installation as they are included with RHACM.

For environments without RHACM, install the required operators:

```bash
# Install tasty tool for operator management
curl -s -L https://github.com/karmab/tasty/releases/download/v0.4.0/tasty-linux-amd64 > ./tasty
sudo install -m 0755 -o root -g root ./tasty /usr/local/bin/tasty

# Install Assisted Service and Hive operators
tasty install assisted-service-operator hive-operator
```

### Step 3: Configure Agent Service

Create the AgentServiceConfig to set up the Infrastructure Operator:

```bash
# Set configuration variables
export DB_VOLUME_SIZE="20Gi"
export FS_VOLUME_SIZE="20Gi"
export OCP_VERSION="4.15.5"
export OCP_MAJMIN=${OCP_VERSION%.*}
export ARCH="x86_64"

# Get release information
export OCP_RELEASE_VERSION=$(curl -s https://mirror.openshift.com/pub/openshift-v4/${ARCH}/clients/ocp/${OCP_VERSION}/release.txt | awk '/machine-os / { print $2 }')
export ISO_URL="https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/${OCP_MAJMIN}/${OCP_VERSION}/rhcos-${OCP_VERSION}-${ARCH}-live.${ARCH}.iso"
export ROOT_FS_URL="https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/${OCP_MAJMIN}/${OCP_VERSION}/rhcos-${OCP_VERSION}-${ARCH}-live-rootfs.${ARCH}.img"

# Create AgentServiceConfig
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
```

!!! warning "Storage Requirements"

    Ensure your management cluster has a default storage class configured. The AgentServiceConfig requires persistent storage for the database and filesystem components.

### Step 4: Configure DNS

Set up DNS entries for your hosted cluster:

```bash
# Example DNS configuration for hosted cluster "example" with base domain "lab.example.com"
# These entries should point to your management cluster nodes or load balancer

# API endpoints
api.example.lab.example.com.      IN A 192.168.1.10
api.example.lab.example.com.      IN A 192.168.1.11
api.example.lab.example.com.      IN A 192.168.1.12
api-int.example.lab.example.com.  IN A 192.168.1.10
api-int.example.lab.example.com.  IN A 192.168.1.11
api-int.example.lab.example.com.  IN A 192.168.1.12

# Application wildcard
*.apps.example.lab.example.com.   IN A 192.168.1.20
```

!!! tip "DNS Planning"

    The API Server for hosted clusters is exposed via NodePort services. DNS entries must point to nodes in your management cluster that can receive this traffic.

### Step 5: Create Your First Hosted Cluster

Configure and create your Agent hosted cluster:

```bash
# Set cluster configuration
export CLUSTERS_NAMESPACE="clusters"
export HOSTED_CLUSTER_NAME="example"
export HOSTED_CONTROL_PLANE_NAMESPACE="${CLUSTERS_NAMESPACE}-${HOSTED_CLUSTER_NAME}"
export BASE_DOMAIN="lab.example.com"
export PULL_SECRET_FILE="$HOME/pull-secret.json"
export OCP_RELEASE="4.15.5-x86_64"
export MACHINE_CIDR="192.168.1.0/24"

# Create the hosted control plane namespace (required for Agent clusters)
oc create namespace ${HOSTED_CONTROL_PLANE_NAMESPACE}

# Create the hosted cluster
hypershift create cluster agent \
  --name=${HOSTED_CLUSTER_NAME} \
  --pull-secret=${PULL_SECRET_FILE} \
  --agent-namespace=${HOSTED_CONTROL_PLANE_NAMESPACE} \
  --base-domain=${BASE_DOMAIN} \
  --api-server-address=api.${HOSTED_CLUSTER_NAME}.${BASE_DOMAIN} \
  --release-image=quay.io/openshift-release-dev/ocp-release:${OCP_RELEASE}
```

### Step 6: Monitor Control Plane Creation

```bash
# Monitor hosted control plane deployment (takes 5-10 minutes)
oc get pods -n ${HOSTED_CONTROL_PLANE_NAMESPACE} -w

# Check hosted cluster status
oc get hostedcluster -n ${CLUSTERS_NAMESPACE}

# Example ready state shows control plane is available:
# NAME      VERSION   KUBECONFIG                PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
# example   4.15.5    example-admin-kubeconfig  Completed  True        False         The hosted control plane is available
```

### Step 7: Create InfraEnv for Agent Discovery

Create an InfraEnv to generate the discovery ISO:

```bash
# Set SSH key for agent access
export SSH_PUB_KEY=$(cat $HOME/.ssh/id_rsa.pub)

# Create InfraEnv
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

# Get the discovery ISO URL
oc get InfraEnv ${HOSTED_CLUSTER_NAME} -n ${HOSTED_CONTROL_PLANE_NAMESPACE} -o jsonpath="{.status.isoDownloadURL}"
```

### Step 8: Add Worker Nodes

Choose your method for adding worker nodes:

=== "Manual Boot Process"

    **For VMs or manual bare metal provisioning:**

    ```bash
    # Download the discovery ISO
    ISO_URL=$(oc get InfraEnv ${HOSTED_CLUSTER_NAME} -n ${HOSTED_CONTROL_PLANE_NAMESPACE} -o jsonpath="{.status.isoDownloadURL}")
    curl -L "$ISO_URL" -o discovery.iso

    # Boot your worker nodes with this ISO
    # Nodes will automatically register as Agents
    ```

    **Approve and configure agents:**

    ```bash
    # Check discovered agents
    oc get agents -n ${HOSTED_CONTROL_PLANE_NAMESPACE}

    # Approve and configure each agent
    oc patch agent <AGENT_ID> -n ${HOSTED_CONTROL_PLANE_NAMESPACE} \
      -p '{"spec":{"installation_disk_id":"/dev/sda","approved":true,"hostname":"worker-0.example.lab.example.com"}}' \
      --type merge
    ```

=== "Automated with Metal3 (Bare Metal)"

    **For bare metal hosts with BMC access:**

    ```bash
    # Configure bare metal operator to watch all namespaces
    oc patch provisioning provisioning-configuration \
      --type merge \
      -p '{"spec":{"watchAllNamespaces": true}}'

    # Wait for metal3 pod to restart
    until oc wait -n openshift-machine-api \
      $(oc get pods -n openshift-machine-api -l baremetal.openshift.io/cluster-baremetal-operator=metal3-state -o name) \
      --for condition=containersready --timeout 10s >/dev/null 2>&1; do sleep 1; done
    ```

    **Create BMC credentials and BareMetalHost:**

    ```bash
    # Set BMC configuration
    export BMC_USERNAME=$(echo -n "root" | base64 -w0)
    export BMC_PASSWORD=$(echo -n "calvin" | base64 -w0)
    export BMC_IP="192.168.1.100"
    export WORKER_NAME="worker-0"
    export BOOT_MAC_ADDRESS="aa:bb:cc:dd:ee:ff"
    export UUID="1"
    export REDFISH_SCHEME="redfish-virtualmedia"

    # Create BMC secret
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

    # Create BareMetalHost
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
        address: ${REDFISH_SCHEME}://${BMC_IP}/redfish/v1/Systems/${UUID}
        credentialsName: ${WORKER_NAME}-bmc-secret
      bootMACAddress: ${BOOT_MAC_ADDRESS}
      online: true
    EOF
    ```

### Step 9: Scale NodePool and Access Cluster

```bash
# Scale the NodePool to add worker nodes
oc scale nodepool/${HOSTED_CLUSTER_NAME} \
  --namespace ${CLUSTERS_NAMESPACE} \
  --replicas=2

# Monitor agent installation progress
oc get agents -n ${HOSTED_CONTROL_PLANE_NAMESPACE} -w

# Generate kubeconfig for hosted cluster
hypershift create kubeconfig \
  --namespace ${CLUSTERS_NAMESPACE} \
  --name ${HOSTED_CLUSTER_NAME} > ${HOSTED_CLUSTER_NAME}.kubeconfig

# Access your hosted cluster
export KUBECONFIG=${HOSTED_CLUSTER_NAME}.kubeconfig
oc get nodes
oc get clusterversion
```

🎉 **Your Agent hosted cluster is ready!** You now have OpenShift running on your bare metal infrastructure.

## Advanced Configuration

### Configure Ingress for Applications

Set up ingress for your applications using MetalLB:

```bash
# Install MetalLB operator in hosted cluster
cat <<"EOF" | oc apply -f -
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
EOF

# Configure MetalLB with IP pool
export INGRESS_IP="192.168.1.20"

envsubst <<"EOF" | oc apply -f -
apiVersion: metallb.io/v1beta1
kind: MetalLB
metadata:
  name: metallb
  namespace: metallb
---
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

# Create LoadBalancer service for ingress
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
```

### Enable Auto-Scaling

Configure automatic node scaling based on demand:

```bash
# Enable auto-scaling (min 2, max 5 nodes)
oc patch nodepool ${HOSTED_CLUSTER_NAME} \
  --namespace ${CLUSTERS_NAMESPACE} \
  --type=json \
  -p '[{"op": "remove", "path": "/spec/replicas"},{"op":"add", "path": "/spec/autoScaling", "value": { "max": 5, "min": 2 }}]'
```

### Create Heterogeneous Node Pools

Add different types of worker nodes:

```bash
# Create a high-memory nodepool
oc apply -f - <<EOF
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: ${HOSTED_CLUSTER_NAME}-highmem
  namespace: ${CLUSTERS_NAMESPACE}
spec:
  clusterName: ${HOSTED_CLUSTER_NAME}
  replicas: 1
  management:
    autoRepair: false
    upgradeType: Replace
  platform:
    type: Agent
    agent:
      agentLabelSelector:
        matchLabels:
          node-role: high-memory
EOF
```

👉 **Complete Guide**: [Create Heterogeneous NodePools](../how-to/agent/create-heterogeneous-nodepools.md)

## Common Next Steps

Once your cluster is running, you might want to:

### Deploy Applications

```bash
# Create a test application
oc new-project test-app
oc new-app --image=quay.io/quay/busybox:latest --name=agent-test
oc get pods -w
```

### Access Advanced Features

- **[Heterogeneous NodePools](../how-to/agent/create-heterogeneous-nodepools.md)**: Different hardware configurations per NodePool
- **[Other SDN Providers](../how-to/agent/other-sdn-providers.md)**: Alternative networking solutions
- **[Exposing HCP Services](../how-to/agent/exposing-services-from-hcp.md)**: Custom service exposure patterns
- **[Global Pull Secret](../how-to/agent/global-pull-secret.md)**: Configure global registry access

## Troubleshooting

### Common Issues

??? question "Agents not discovering"

    **Symptoms**: No agents appear after booting with discovery ISO

    **Solutions**:
    ```bash
    # Check InfraEnv status
    oc describe InfraEnv ${HOSTED_CLUSTER_NAME} -n ${HOSTED_CONTROL_PLANE_NAMESPACE}

    # Verify network connectivity from booted hosts
    # Check DHCP or static IP configuration
    # Ensure management cluster is reachable from worker hosts

    # Check assisted-service logs
    oc logs -n assisted-installer deployment/assisted-service
    ```

??? question "BMC connection failures"

    **Symptoms**: BareMetalHost stuck in error state

    **Solutions**:
    ```bash
    # Check BMC credentials and connectivity
    oc describe bmh ${WORKER_NAME} -n ${HOSTED_CONTROL_PLANE_NAMESPACE}

    # Verify Redfish endpoint accessibility
    curl -k -u username:password https://${BMC_IP}/redfish/v1/Systems/

    # Check metal3 operator logs
    oc logs -n openshift-machine-api deployment/metal3-baremetal-operator
    ```

??? question "DNS resolution issues"

    **Symptoms**: Cannot access hosted cluster API or applications

    **Solutions**:
    ```bash
    # Verify DNS configuration
    nslookup api.${HOSTED_CLUSTER_NAME}.${BASE_DOMAIN}
    nslookup test-app.apps.${HOSTED_CLUSTER_NAME}.${BASE_DOMAIN}

    # Check NodePort services in management cluster
    oc get svc -n ${HOSTED_CONTROL_PLANE_NAMESPACE} | grep NodePort

    # Verify load balancer or ingress configuration
    ```

??? question "Storage provisioning failures"

    **Symptoms**: AgentServiceConfig or hosted cluster components failing

    **Solutions**:
    ```bash
    # Check default storage class
    oc get storageclass

    # Verify PVC status
    oc get pvc -n assisted-installer
    oc get pvc -n ${HOSTED_CONTROL_PLANE_NAMESPACE}

    # Check storage operator status
    oc get csv -n openshift-storage
    ```

### Get Help

- 📚 **Agent-specific guides**: [How-to guides](../how-to/agent/)
- 💬 **Community**: [HyperShift Slack](https://kubernetes.slack.com/channels/hypershift)
- 🐛 **Bug reports**: [Red Hat Customer Portal](https://issues.redhat.com/)
- 🔗 **Assisted Installer**: [Infrastructure Operator Documentation](https://github.com/openshift/assisted-service)

## Cleanup

When you're done experimenting:

```bash
# Delete the hosted cluster
hypershift destroy cluster agent \
  --name ${HOSTED_CLUSTER_NAME} \
  --namespace ${CLUSTERS_NAMESPACE}

# Clean up InfraEnv and agents
oc delete InfraEnv ${HOSTED_CLUSTER_NAME} -n ${HOSTED_CONTROL_PLANE_NAMESPACE}
oc delete agents --all -n ${HOSTED_CONTROL_PLANE_NAMESPACE}

# Clean up BareMetalHosts if used
oc delete bmh --all -n ${HOSTED_CONTROL_PLANE_NAMESPACE}

# Remove namespace
oc delete namespace ${HOSTED_CONTROL_PLANE_NAMESPACE}
```

!!! warning "Infrastructure Cleanup"

    Agent platform cleanup only removes the software components. Physical infrastructure (servers, networking) must be manually reset or reprovisioned for reuse.

---

🎉 **Congratulations!** You now have a working HyperShift hosted cluster on the Agent platform. This setup provides maximum flexibility for bare metal, edge, and air-gapped deployments.