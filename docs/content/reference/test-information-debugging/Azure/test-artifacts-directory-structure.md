# ARO HCP E2E Test Artifacts Navigation Guide

This document serves as a roadmap for understanding and navigating ARO HCP (Azure Red Hat OpenShift Hosted Control Planes) end-to-end test artifacts. Use this guide to quickly locate specific components, logs, and debugging information.

## Quick Navigation Index

### 🔍 Finding Hosted Control Plane Components
- **Control Plane Pod Deployments**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/e2e-clusters-*/apps/deployments/`
- **Control Plane Pod Logs**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/e2e-clusters-*/core/pods/logs/`
- **Control Plane Pod Manifests**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/e2e-clusters-*/core/pods/`

### 🎛️ Finding HyperShift Operator Components
- **HyperShift Operator Deployment**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/hypershift/apps/deployments/operator.yaml`
- **External DNS Deployment**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/hypershift/apps/deployments/external-dns.yaml`
- **Operator Logs**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/hypershift/core/pods/logs/operator-*-operator.log`
- **External DNS Logs**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/hypershift/core/pods/logs/external-dns-*-external-dns.log`

### 🎯 Key Control Plane Components to Look For

#### Core Kubernetes Control Plane (Basic cluster functionality)
These components form the foundation of any Kubernetes cluster. If these fail, the entire cluster is non-functional:

- **etcd**: `etcd-0.yaml` and `etcd-0-*.log` files
    - *Why*: Stores all cluster state and configuration. Failures here cause complete cluster outages
    - *Look for*: Connection issues, disk space, quorum problems, backup/restore errors

- **kube-apiserver**: `kube-apiserver-*.yaml` and `kube-apiserver-*-*.log` files  
    - *Why*: The central API endpoint for all cluster operations. Nothing works without it
    - *Look for*: TLS certificate issues, etcd connectivity, authentication/authorization failures

- **kube-controller-manager**: `kube-controller-manager-*.yaml` and `kube-controller-manager-*-*.log` files
     - *Why*: Runs core controllers (ReplicaSets, Deployments, etc.). Controls workload lifecycle
    - *Look for*: Resource reconciliation failures, cloud provider integration issues

- **kube-scheduler**: `kube-scheduler-*.yaml` and `kube-scheduler-*-*.log` files
    - *Why*: Places pods on nodes. Scheduling failures prevent workloads from running
    - *Look for*: Resource constraints, node affinity issues, scheduling policy problems

#### OpenShift-Specific Components (OpenShift functionality)
These add OpenShift features on top of Kubernetes. Required for OpenShift-specific capabilities:

- **OpenShift API Server**: `openshift-apiserver-*.yaml` and `openshift-apiserver-*-*.log` files
    - *Why*: Handles OpenShift-specific APIs (Routes, BuildConfigs, etc.)
    - *Look for*: Custom resource failures, OpenShift feature unavailability

- **OAuth Server**: `oauth-openshift-*.yaml` and `oauth-openshift-*-*.log` files
    - *Why*: Manages authentication and user access to the cluster
    - *Look for*: Login failures, identity provider issues, token problems

#### Node Management & Infrastructure (Node lifecycle and joining)
These components manage the infrastructure and node lifecycle. Critical for scaling and node operations:

- **Control Plane Operator**: `control-plane-operator-*.yaml` and `control-plane-operator-*-*.log` files
    - *Why*: HyperShift-specific component that manages the hosted control plane lifecycle
    - *Look for*: Control plane component deployment issues, version management problems

- **Cluster API Manager**: `cluster-api-*.yaml` and `cluster-api-*-*.log` files
    - *Why*: Core CAPI controller that manages infrastructure resources and machine lifecycle
    - *Look for*: Machine creation/deletion failures, cluster infrastructure issues

- **CAPI Provider**: `capi-provider-*.yaml` and `capi-provider-*-*.log` files
    - *Why*: Azure-specific implementation that provisions VMs, networking, and storage
     - *Look for*: Azure API failures, resource quota issues, networking problems

- **Ignition Server**: `ignition-server-*.yaml` and `ignition-server-*-*.log` files
    - *Why*: Provides initial configuration to new nodes during bootstrap process
    - *Look for*: Node configuration failures, network connectivity from nodes to control plane

- **Machine Approver**: `machine-approver-*.yaml` and `machine-approver-*-*.log` files
    - *Why*: Automatically approves Certificate Signing Requests from nodes joining the cluster
    - *Look for*: CSR approval failures, certificate issues preventing node join

### 🏁 Start Here: Primary Resources for Debugging
**Always check these HyperShift custom resources first** 

- their status sections provide high-level cluster state:
    - **HostedCluster**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/e2e-clusters-*/hypershift.openshift.io/hostedclusters/*.yaml`
    - **HostedControlPlane**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/e2e-clusters-*-{test-name}-*/hypershift.openshift.io/hostedcontrolplanes/*.yaml`
    - **NodePool**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/e2e-clusters-*/hypershift.openshift.io/nodepools/*.yaml`

💡 **Why check these first**: The `status` sections contain:

- Overall cluster readiness and conditions
- Infrastructure provisioning status  
- Control plane component health
- Node pool scaling and readiness
- Error messages and failure reasons

### 📂 Top-Level Test Execution Files
- `build-log.txt` - Main CI build execution log
- `clone-log.txt` - Git repository cloning logs  
- `finished.json` - Test completion status with success/failure details
- `prowjob.json` - Complete Prow job specification (PR info, test configuration)
- `podinfo.json` - CI pod execution information

## 📋 Understanding Test Results

Each test directory (i.e. `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*`) represents a different validation scenario. 

Check the `junit.xml` files for test pass/fail status (i.e. `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/junit.xml`), and use the corresponding namespace directories to drill down into specific component failures.

The artifact structure provides comprehensive debugging capabilities from high-level test orchestration down to individual container logs within the hosted control plane components.

### 🧪 Available Test Scenarios
- `TestAutoscaling/` - Cluster autoscaling validation
- `TestCreateCluster/` - Basic hosted cluster creation  
- `TestCreateClusterCustomConfig/` - Custom configuration testing
- `TestHAEtcdChaos/` - etcd high availability and chaos testing
- `TestNodePool_HostedCluster*/` - Node pool lifecycle management
- `TestUpgradeControlPlane/` - Control plane upgrade procedures

### 📈 Test Results & Metrics
- `e2e-metrics-raw.prometheus` - Raw Prometheus metrics from test execution
- `junit.xml` - JUnit test results summary

### `/artifacts/release/` - Release Management
- `artifacts/release-images-n2minor` - Release image artifacts and metadata
- `build-log.txt` - Release build logs

## Directory Structure Deep Dive

### `/artifacts/` - Main Test Artifacts Directory

#### `/artifacts/build-logs/` - Component Build Logs
Contains compilation logs for the core HyperShift components:

- `hypershift-amd64.log` - Main HyperShift binary build
- `hypershift-operator-amd64.log` - HyperShift operator build  
- `hypershift-tests-amd64.log` - Test suite compilation

#### `/artifacts/build-resources/` - Build-Time Kubernetes Resources
Resource manifests generated during the build process:

- `builds.json` - OpenShift build configurations
- `events.json` - Kubernetes events during build
- `pods.json` - Pod specifications and status

#### `/artifacts/ci-operator-*` - CI Pipeline Artifacts
CI operator execution details:

- `ci-operator.log` - Main CI execution log (check here for high-level test failures)
- `ci-operator-metrics.json` - Performance metrics and timing
- `junit_operator.xml` - JUnit test results for CI integration

#### `/artifacts/e2e-aks/` - Azure E2E Test Execution

This directory contains the complete AKS-based test execution pipeline:

##### 🏗️ Infrastructure Management
- `aks-provision/` - AKS management cluster creation logs
- `aks-deprovision/` - AKS cluster cleanup and teardown logs  
- `azure-deprovision-resourcegroup/` - Azure resource group cleanup

##### 🔐 Security and Access Setup
- `hypershift-azure-aks-attach-kv/` - Azure Key Vault integration for secrets
- `hypershift-install/` - HyperShift operator installation on management cluster
- `ipi-install-rbac/` - RBAC setup for infrastructure provisioning
- `openshift-cluster-bot-rbac/` - Service account permissions for automation

##### 🧪 Main Test Execution
- `hypershift-azure-run-e2e/` - **⭐ PRIMARY TEST DIRECTORY** containing all hosted control plane tests

### 📊 Inside `hypershift-azure-run-e2e/artifacts/`

This is where you'll find the **hosted control plane components and logs**:

#### Test Case Structure
Each `Test*/` directory represents a different test scenario and contains:

##### 📁 Per-Test Artifacts
- `create.log` - **Hosted cluster creation logs** (start here for provisioning issues)
- `destroy.log` - Cluster cleanup logs
- `dump.log` - Complete resource dump (comprehensive debugging info)
- `infrastructure.log` - Azure infrastructure provisioning logs
- `hostedcluster.tar` - Complete hosted cluster configuration archive

##### 🗂️ Namespace Organization (`namespaces/` directory)
**Key namespace patterns to look for:**

###### `hypershift/` - **🎛️ HyperShift Operator Namespace**
This namespace contains the management cluster components that orchestrate hosted control planes:

**Core Components:**

- `apps/deployments/operator.yaml` - **HyperShift operator deployment** (manages HostedClusters)
- `apps/deployments/external-dns.yaml` - **External DNS deployment** (Azure DNS integration)

**Pod Manifests & Logs:**

- `core/pods/operator-*.yaml` - HyperShift operator pod specification
- `core/pods/external-dns-*.yaml` - External DNS pod specification
- `core/pods/logs/operator-*-operator.log` - **🔥 Main HyperShift operator logs** (HostedCluster reconciliation)
- `core/pods/logs/operator-*-init-environment.log` - Operator initialization logs
- `core/pods/logs/external-dns-*-external-dns.log` - **DNS management logs** (Azure DNS record creation/updates)

**Configuration & Events:**

- `core/configmaps/` - Operator configuration (feature gates, supported versions)
- `core/events/operator-*.yaml` - Operator lifecycle events
- `core/events/external-dns-*.yaml` - DNS operation events
- `core/services/operator.yaml` - Operator service definition

💡 **Key Use Cases:**

- **HostedCluster issues**: Check `operator-*-operator.log` for cluster provisioning/reconciliation errors
- **DNS problems**: Review `external-dns-*-external-dns.log` for Azure DNS integration issues
- **Operator crashes**: Look at operator events and initialization logs

###### `hypershift-sharedingress/` - Shared Ingress Infrastructure
- Router and ingress controller components

###### `e2e-clusters-*-{test-name}-*/` - **⭐ HOSTED CONTROL PLANE NAMESPACE**

This is where the **actual control plane pods run**. Look here for:

- `core/pods/` - **Control plane pod manifests** (etcd, kube-apiserver, etc.)
- `core/pods/logs/` - **🔥 Control plane pod logs** (primary debugging location)
- `apps/deployments/` - Control plane component deployments
- `hypershift.openshift.io/` - HyperShift custom resources (HostedControlPlane, etc.)

## 🛠️ Debugging Workflows

### Finding Control Plane Issues
1. **Start with overall test status**: Check `finished.json` for high-level failure info
2. **Check HyperShift custom resources**: Examine HostedCluster, HostedControlPlane, and NodePool status sections for error conditions
3. **Check test execution**: Look at `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/create.log` 
4. **Find control plane pods**: Navigate to `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/namespaces/e2e-clusters-*-{test-name}-*/core/pods/`
5. **Review specific component logs**: Check `core/pods/logs/{component-name}-*-{container}.log`

### Node Joining and Infrastructure Issues
1. **Check NodePool status**: Look for scaling, readiness, and machine creation issues
2. **CAPI components**: Review `cluster-api-*` and `capi-provider-*` logs for Azure infrastructure problems
3. **Node bootstrapping**: Check `ignition-server-*` logs for node configuration issues
4. **CSR approval**: Review `machine-approver-*` logs for certificate signing problems
5. **Control plane coordination**: Check `control-plane-operator-*` logs for component management issues

### Common Log Locations

#### Hosted Control Plane Components (in `e2e-clusters-*` namespaces)
- **etcd logs**: `etcd-0-etcd.log`, `etcd-0-healthz.log`, `etcd-0-etcd-metrics.log`
- **API Server logs**: `kube-apiserver-*-kube-apiserver.log`, `kube-apiserver-*-audit-logs.log`
- **Controller Manager**: `kube-controller-manager-*-kube-controller-manager.log`
- **Scheduler**: `kube-scheduler-*-kube-scheduler.log`

#### Management Cluster Components (in `hypershift/` namespace)
- **HyperShift Operator**: `operator-*-operator.log` (HostedCluster/HostedControlPlane reconciliation)
- **External DNS**: `external-dns-*-external-dns.log` (Azure DNS record management)
- **Operator Initialization**: `operator-*-init-environment.log` (startup and environment setup)

### Infrastructure Debugging
- **AKS provisioning**: `artifacts/e2e-aks/aks-provision/build-log.txt`
- **Azure resources**: `artifacts/e2e-aks/hypershift-azure-run-e2e/artifacts/Test*/infrastructure.log`
- **Network setup**: Look for `cloud-network-config-controller` logs in hosted control plane namespace

### HyperShift Operator Debugging
1. **HostedCluster creation issues**: Check `hypershift/core/pods/logs/operator-*-operator.log` for reconciliation errors
2. **DNS resolution problems**: Review `hypershift/core/pods/logs/external-dns-*-external-dns.log` for Azure DNS failures
3. **Operator startup issues**: Examine `hypershift/core/pods/logs/operator-*-init-environment.log` and operator events
4. **Cross-reference with control plane**: If operator reports success but control plane fails, check hosted control plane namespace logs

### External DNS Specific Issues
- **Azure DNS integration**: External DNS manages DNS records for hosted cluster API endpoints
- **Common failure patterns**: Permission issues with Azure DNS zones, rate limiting, DNS propagation delays
- **Log format**: Look for Azure API calls, DNS record operations, and error responses in external-dns logs
