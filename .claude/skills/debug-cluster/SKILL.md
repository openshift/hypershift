---
name: Debug Cluster
description: "Provides systematic debugging approaches for HyperShift hosted-cluster issues. Auto-applies when debugging cluster problems, investigating stuck deletions, or troubleshooting control plane issues."
---

# HyperShift Cluster Debugging Guide

This skill provides structured debugging workflows for common HyperShift hosted-cluster issues.

## When to Use This Skill

This skill automatically applies when:
- Investigating hosted-cluster deletion issues
- Debugging stuck resources or finalizers
- Troubleshooting control plane problems
- Analyzing NodePool lifecycle issues
- Reviewing operator logs for cluster problems

## Provider-Specific Troubleshooting

For provider-specific issues and detailed troubleshooting steps, refer to these subskills:

- **AWS**: [aws-troubleshooting.md](./aws-troubleshooting.md) - AWS-specific CAPI issues, infrastructure cleanup, and reinstallation

The main skill below provides provider-agnostic debugging workflows. When you encounter provider-specific issues, consult the relevant subskill for detailed resolution steps.

## Key Components to Understand

### Resource Hierarchy
- **HostedCluster (HC)**: Main cluster resource in the management cluster
- **HostedControlPlane (HCP)**: Control plane representation of the HC in HCP namespace
- **NodePool (NP)**: Worker node pool resources
- **CAPI Resources**: Cluster API resources (Cluster, Machine, etc.) in HCP namespace

### Operators
- **hypershift-operator (HO)**: Manages HC and NP resources
- **control-plane-operator (CPO)**: Manages HCP and control plane components
- **hosted-cluster-config-operator (HCCO)**: Manages configuration and in-cluster resources for hosted clusters

### Namespaces
- **HC namespace**: Where HostedCluster and NodePool resources live (e.g., `default`, `clusters`)
- **HCP namespace**: Where control plane pods and CAPI resources run (e.g., `clusters-<cluster-name>`)

## Common Debugging Scenarios

### Scenario: Hosted Cluster Stuck Deleting

When a hosted-cluster is stuck in deleting state, follow this systematic debugging process:

#### 1. Node Pools Deletion
Check and verify NodePool deletion is progressing:

```bash
# Check NodePool resources in HC namespace
kubectl get nodepool -n <hc-namespace>

# Check CAPI cluster resource status in HCP namespace
kubectl get cluster -n <hcp-namespace> -o yaml

# Check CAPI provider pod logs
kubectl logs -n <hcp-namespace> deployment/capi-provider

# Check CAPI machines status in HCP namespace
kubectl get machines -n <hcp-namespace>
kubectl describe machines -n <hcp-namespace>

# Review HyperShift operator logs for NodePool issues
kubectl logs -n hypershift deployment/operator --tail=100 | grep -i nodepool
kubectl logs -n hypershift deployment/operator --tail=100 | grep -i <cluster-name>
```

**What to look for:**
- Finalizers blocking NodePool deletion
- CAPI machines that aren't terminating
- Provider errors preventing machine deletion
- HO logs showing reconciliation errors

#### 2. HostedControlPlane Resource Deletion
Verify HCP resource and pods are being cleaned up:

```bash
# Check HCP resource status
kubectl get hostedcontrolplane -n <hcp-namespace> -o yaml

# Check pods in HCP namespace
kubectl get pods -n <hcp-namespace>

# Check for stuck pods
kubectl get pods -n <hcp-namespace> --field-selector=status.phase!=Running

# Review control-plane-operator logs
kubectl logs -n <hcp-namespace> deployment/control-plane-operator --tail=100
```

**What to look for:**
- HCP finalizers blocking deletion
- Pods with finalizers or in Terminating state
- CPO logs showing errors in resource cleanup
- PVC or other resources preventing namespace deletion

#### 3. HCP Namespace Deletion
Investigate why the HCP namespace isn't being removed:

```bash
# Check namespace status
kubectl get namespace <hcp-namespace> -o yaml

# List all remaining resources in namespace
kubectl api-resources --verbs=list --namespaced -o name | \
  xargs -n 1 kubectl get --show-kind --ignore-not-found -n <hcp-namespace>

# Check for resources with finalizers
kubectl get all -n <hcp-namespace> -o json | \
  jq '.items[] | select(.metadata.finalizers != null) | {kind: .kind, name: .metadata.name, finalizers: .metadata.finalizers}'

# Review HO logs for namespace cleanup
kubectl logs -n hypershift deployment/operator --tail=100 | grep -i namespace
```

**What to look for:**
- Resources with finalizers preventing deletion
- API resources that HO should have cleaned up
- Webhook or admission controller errors
- Namespace stuck in Terminating state

#### 4. HostedCluster Resource Deletion
Final check on the HostedCluster resource itself:

```bash
# Check HostedCluster status
kubectl get hostedcluster -n <hc-namespace> <cluster-name> -o yaml

# Check HostedCluster finalizers
kubectl get hostedcluster -n <hc-namespace> <cluster-name> -o jsonpath='{.metadata.finalizers}'

# Review HO logs for HostedCluster deletion
kubectl logs -n hypershift deployment/operator --tail=200 | grep -i "hostedcluster.*<cluster-name>"
```

**What to look for:**
- Finalizers blocking HostedCluster deletion
- HO errors in reconciliation loop
- Dependencies that haven't been cleaned up
- Cloud resources that failed to delete

#### Quick Debugging Checklist

When investigating cluster deletion issues:
- [ ] Check resource status and conditions
- [ ] Review relevant operator logs (HO, CPO)
- [ ] Inspect finalizers on stuck resources
- [ ] Verify CAPI resources are reconciling
- [ ] Check for events indicating failures
- [ ] Look for provider errors
- [ ] Verify namespace cleanup progress
- [ ] Check for webhook or admission errors

#### Common Issues and Resolutions

##### Issue: NodePool won't delete
- **Cause**: CAPI machines stuck due to provider errors
- **Resolution**: Check provider credentials, investigate machine deletion errors in HO logs

##### Issue: Machines stuck in "Deleting" phase with "WaitingForInfrastructureDeletion"
- **Cause**: Cluster resource deleted before machine resources, blocking CAPI controller reconciliation
- **Root Cause**: CAPI machine controllers require the cluster resource to be "ready" to proceed with deletion
- **Symptoms**:
  - Machines show `InfrastructureReady: 1 of 2 completed`
  - CAPI logs show cluster resource is not ready
  - Instances still running but controller can't terminate them
- **Resolution**: Provider-specific cleanup is required. See provider-specific troubleshooting:
  - **AWS**: See [aws-troubleshooting.md](./aws-troubleshooting.md) for detailed resolution steps

##### Issue: HCP namespace stuck in Terminating
- **Cause**: Resources with finalizers or failing webhooks
- **Resolution**: List all resources in namespace, remove finalizers from orphaned resources, check webhook availability

##### Issue: HostedCluster stuck with finalizers
- **Cause**: Dependencies not fully cleaned up (NodePools, HCP, cloud resources)
- **Resolution**: Verify all dependent resources are deleted, check HO logs for reconciliation errors

##### Issue: Control plane pods not terminating
- **Cause**: PVCs or other resources with protection finalizers
- **Resolution**: Check PVC status, review storage class finalizers, inspect CPO logs

### Scenario: HyperShift CRDs Missing or Corrupted

When HyperShift CRDs are accidentally deleted or corrupted (e.g., after using `hypershift destroy infra`), you'll need to reinstall HyperShift.

**⚠️ WARNING: HyperShift reinstallation should be a last resort.** Only proceed if CRDs are genuinely missing or corrupted and cannot be recovered through other means. Reinstallation will cause downtime and may impact existing hosted-clusters.

#### Symptoms:
- Commands like `kubectl get hostedclusters` fail with: `error: the server doesn't have a resource type "hostedclusters"`
- HyperShift operator logs show errors about missing CRDs
- CRDs for HostedCluster, NodePool, or CAPI resources are missing

#### Check for Missing CRDs:
```bash
# Check if critical HyperShift CRDs exist
kubectl get crd hostedclusters.hypershift.openshift.io
kubectl get crd nodepools.hypershift.openshift.io

# Count HyperShift CRDs (should be ~9)
kubectl get crd | grep hypershift | wc -l

# Count CAPI CRDs (should be ~50)
kubectl get crd | grep cluster.x-k8s.io | wc -l
```

#### Resolution: Reinstall HyperShift

**🤖 AI Assistant Note:** When this scenario is encountered, Claude should guide and suggest the reinstallation steps to the user but NEVER execute the reinstallation commands itself. The user must explicitly run these commands. Provide clear instructions and explanations, but do not use the Bash tool to perform the actual reinstallation.

##### Step 1: Gather Required Parameters
```bash
# You'll need these for reinstallation:
# - OIDC storage provider configuration (provider-specific, see below)
# - Provider credentials (if applicable)
# - Any custom configuration flags used in original installation
```

**Provider-specific parameters:**
- **AWS**: See [aws-troubleshooting.md](./aws-troubleshooting.md) for AWS-specific OIDC S3 parameters

##### Step 2: Completely Uninstall HyperShift
```bash
hypershift install render | kubectl delete -f -
```

This will:
- Remove the HyperShift operator deployment
- Delete all HyperShift CRDs (HostedCluster, NodePool, etc.)
- Clean up RBAC resources, webhooks, and other components

##### Step 3: Reinstall HyperShift
```bash
hypershift install \
  [provider-specific-flags] \
  --enable-defaulting-webhook true
```

Add any other flags that were part of your original installation.

**Provider-specific installation:**
- **AWS**: See [aws-troubleshooting.md](./aws-troubleshooting.md) for AWS-specific installation example

##### Step 4: Verify Installation
```bash
# Check operator is running
kubectl get deploy -n hypershift
kubectl get pods -n hypershift

# Verify CRDs are installed
kubectl get crd | grep hostedcluster
kubectl get crd | grep nodepool
kubectl get crd | grep cluster.x-k8s.io | wc -l

# Test API accessibility
kubectl get hostedclusters -A

# Check operator logs for errors
kubectl logs -n hypershift deployment/operator --tail=50

# Verify controllers are running
kubectl logs -n hypershift deployment/operator --tail=100 | grep "Starting workers"
```

**Expected Results After Reinstallation:**
- Operator deployment: 2/2 READY
- HostedCluster CRD: Present
- NodePool CRD: Present
- CAPI CRDs: ~50 installed
- HyperShift CRDs: ~9 total
- Controllers: HostedCluster, NodePool, and other controllers with workers started
- Webhooks: Mutating webhook configured
- API: `kubectl get hostedclusters -A` returns successfully (even if no clusters exist)

**Important Notes:**
- Reinstallation does NOT affect existing hosted-clusters if their resources still exist
- If CRDs were deleted, any existing HostedCluster/NodePool resources are gone
- You may need to recreate hosted-clusters if their definitions were lost
- Ensure you use the same configuration flags as the original installation

## General Debugging Tips

### Checking Finalizers
```bash
# List all finalizers on a resource
kubectl get <resource-type> <name> -n <namespace> -o jsonpath='{.metadata.finalizers}'

# Remove a specific finalizer (use with caution!)
kubectl patch <resource-type> <name> -n <namespace> -p '{"metadata":{"finalizers":null}}' --type=merge
```

### Operator Logs
```bash
# HyperShift operator logs with context
kubectl logs -n hypershift deployment/operator --tail=500 --timestamps

# Control plane operator logs
kubectl logs -n <hcp-namespace> deployment/control-plane-operator --tail=500 --timestamps

# Follow logs in real-time
kubectl logs -n hypershift deployment/operator -f
```

### Resource Events
```bash
# Get events for a specific resource
kubectl describe <resource-type> <name> -n <namespace>

# Get all events in a namespace, sorted by time
kubectl get events -n <namespace> --sort-by='.lastTimestamp'
```

### Conditions and Status
```bash
# Check resource conditions
kubectl get <resource-type> <name> -n <namespace> -o jsonpath='{.status.conditions}' | jq .

# Check specific condition
kubectl get hostedcluster <name> -n <namespace> -o jsonpath='{.status.conditions[?(@.type=="Available")]}'
```

## Additional Resources

- HyperShift operator code: `hypershift-operator/controllers/hostedcluster/`
- Control plane operator code: `control-plane-operator/controllers/`
- API definitions: `api/hypershift/v1beta1/`
- E2E test examples: `test/e2e/`
