# Resource-Based Control Plane Autoscaling

Resource-based control plane autoscaling enables automatic sizing of HostedClusters based on actual Kube API server resource usage rather than worker node count. This feature uses Vertical Pod Autoscaler (VPA) recommendations to determine the optimal cluster size class for a HostedCluster.

## Platform Support

**Important**: This feature is only available for HostedClusters using the request serving isolation architecture on AWS. The feature requires the `dedicated-request-serving-components` topology annotation to be set on the HostedCluster.

## Prerequisites

Before enabling resource-based control plane autoscaling, ensure the following prerequisites are met:

### VPA Operator

The Vertical Pod Autoscaler operator must be installed on the management cluster via Operator Lifecycle Manager (OLM). The VPA operator only supports the `OwnNamespace` install mode, so it must be installed in its own namespace.

#### Step 1: Create the VPA Namespace

Create the namespace for the VPA operator:

```bash
kubectl create namespace openshift-vertical-pod-autoscaler
```

#### Step 2: Create the OperatorGroup

Create an OperatorGroup that targets only the VPA namespace:

```bash
kubectl apply -f - <<EOF
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: vpa-operator-group
  namespace: openshift-vertical-pod-autoscaler
spec:
  targetNamespaces:
    - openshift-vertical-pod-autoscaler
EOF
```

#### Step 3: Create the Subscription

Create a Subscription to install the VPA operator from the `redhat-operators` catalog source:

```bash
kubectl apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: vertical-pod-autoscaler
  namespace: openshift-vertical-pod-autoscaler
spec:
  channel: stable
  name: vertical-pod-autoscaler
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
EOF
```

#### Step 4: Verify Installation

Wait for the ClusterServiceVersion (CSV) to reach the `Succeeded` phase:

```bash
kubectl wait --for=condition=phase=Succeeded \
  csv -n openshift-vertical-pod-autoscaler \
  -l operators.coreos.com/vertical-pod-autoscaler.openshift-vertical-pod-autoscaler \
  --timeout=15m
```

Verify that the VPA CRD is available:

```bash
kubectl get crd verticalpodautoscalers.autoscaling.k8s.io
```

Verify that the VPA recommender deployment is available:

```bash
kubectl wait --for=condition=available \
  deployment/vpa-recommender-default \
  -n openshift-vertical-pod-autoscaler \
  --timeout=15m
```

### VPA Controller Instance

After the VPA operator is installed, a `VerticalPodAutoscalerController` instance named `default` is automatically created in the `openshift-vertical-pod-autoscaler` namespace. You must configure this controller instance for resource-based autoscaling.

Example `VerticalPodAutoscalerController` configuration:

```yaml
apiVersion: autoscaling.openshift.io/v1
kind: VerticalPodAutoscalerController
metadata:
  name: default
  namespace: openshift-vertical-pod-autoscaler
spec:
  recommendationOnly: true
  deploymentOverrides:
    recommender:
      container:
        args:
          - --memory-aggregation-interval=1h
          - --memory-aggregation-interval-count=12
          - --memory-histogram-decay-half-life=1h
```

For a complete list of VPA recommender configuration flags, refer to the [VPA recommender documentation](https://github.com/kubernetes/autoscaler/blob/master/vertical-pod-autoscaler/README.md#recommender).

### ClusterSizingConfiguration

A `ClusterSizingConfiguration` resource named `cluster` must exist in the management cluster with:

- Optional: Size configurations that include `capacity` specifications with memory values. If `capacity` is not specified for a size, the system will introspect the memory and CPU capacity from existing MachineSets in the `openshift-machine-api` namespace. The system looks for MachineSets with the `hypershift.openshift.io/cluster-size` label matching the size name and reads the `machine.openshift.io/memoryMb` and `machine.openshift.io/vCPU` annotations from those MachineSets. The first MachineSet found for each size label is used as the authoritative source for that size's capacity.
- Optional: `resourceBasedAutoscaling.kubeAPIServerMemoryFraction` configuration

## Enabling Resource-Based Autoscaling

To enable resource-based control plane autoscaling for a HostedCluster, add the following annotation:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example-cluster
  namespace: clusters
  annotations:
    hypershift.openshift.io/topology: dedicated-request-serving-components
    hypershift.openshift.io/resource-based-cp-auto-scaling: "true"
```

The feature requires both annotations:
- `hypershift.openshift.io/topology: dedicated-request-serving-components` - Enables request serving isolation architecture
- `hypershift.openshift.io/resource-based-cp-auto-scaling: "true"` - Enables resource-based autoscaling

## Annotations

The following annotations are used by the resource-based control plane autoscaling feature:

### Input Annotations

- **`hypershift.openshift.io/resource-based-cp-auto-scaling`**: Set to `"true"` to enable resource-based autoscaling for the HostedCluster. This annotation must be used in conjunction with the `dedicated-request-serving-components` topology.

- **`hypershift.openshift.io/topology`**: Must be set to `dedicated-request-serving-components` for the feature to work.

### Output Annotations

- **`hypershift.openshift.io/recommended-cluster-size`**: This annotation is automatically set by the autoscaler controller with the recommended cluster size class based on VPA recommendations. The value corresponds to a size name from the `ClusterSizingConfiguration`.

## ClusterSizingConfiguration

The `ClusterSizingConfiguration` resource controls how cluster sizes are determined. For resource-based autoscaling, configure the following:

### Size Capacity Configuration

Each size configuration in `spec.sizes` can include a `capacity` field with memory and cpu specifications:

```yaml
apiVersion: scheduling.hypershift.openshift.io/v1alpha1
kind: ClusterSizingConfiguration
metadata:
  name: cluster
spec:
  sizes:
    - name: small
      criteria:
        from: 0
        to: 10
      capacity:
        memory: 32Gi
        cpu: "8"
    - name: medium
      criteria:
        from: 11
        to: 50
      capacity:
        memory: 64Gi
        cpu: "16"
    - name: large
      criteria:
        from: 51
      capacity:
        memory: 128Gi
        cpu: "32"
```

### Resource-Based Autoscaling Configuration

The `resourceBasedAutoscaling` section allows you to configure how memory recommendations are interpreted:

```yaml
spec:
  resourceBasedAutoscaling:
    kubeAPIServerMemoryFraction: "0.65"
```

- **`kubeAPIServerMemoryFraction`**: A value between 0 and 1 that determines what fraction of a machine's total memory is available for the Kube API server pod. This fraction is used to determine whether a VPA memory recommendation fits within a particular cluster size. If not specified, a default fraction of 0.65 is used.

The autoscaler selects the smallest cluster size for which:
```plaintext
machine_memory * kubeAPIServerMemoryFraction >= VPA_recommended_memory
```

## How It Works

1. When resource-based autoscaling is enabled, the HyperShift operator creates a `VerticalPodAutoscaler` resource targeting the `kube-apiserver` deployment in the control plane namespace.

2. The VPA monitors the kube-apiserver container's resource usage and generates memory recommendations.

3. The autoscaler controller reads the VPA recommendations and determines the appropriate cluster size class based on:
   - The VPA's memory recommendation for the kube-apiserver container
   - The machine memory capacity for each size class (from `ClusterSizingConfiguration`)
   - The configured `kubeAPIServerMemoryFraction`

4. The controller sets the `hypershift.openshift.io/recommended-cluster-size` annotation on the HostedCluster with the recommended size.

5. The hosted cluster sizing controller uses this recommendation (along with other factors) to determine the actual cluster size applied to the HostedCluster.

## Monitoring

You can monitor the autoscaling behavior by checking:

- The `hypershift.openshift.io/recommended-cluster-size` annotation on the HostedCluster
- The VPA resource status in the control plane namespace: `kubectl get vpa kube-apiserver -n <control-plane-namespace>`
- The VPA recommendation conditions and container recommendations

## Troubleshooting

If the recommended cluster size is not being set:

1. Verify that both required annotations are present on the HostedCluster
2. Check that the VPA operator is installed and the VPA controller instance exists
3. Verify that the VPA resource exists in the control plane namespace and has valid recommendations
4. Ensure the `ClusterSizingConfiguration` has size configurations with capacity specifications
5. Check controller logs for errors related to size cache updates or VPA reconciliation

