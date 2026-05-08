# cluster-olm-operator HyperShift Dual-API Implementation Guide

This document describes the code changes needed in the `cluster-olm-operator` repository to support HyperShift's dual-API pattern.

## Overview

In HyperShift deployments, `cluster-olm-operator` must access TWO API servers:

1. **Hosted Cluster API** (via `HOSTED_KUBECONFIG`) - For managing OLM v1 resources:
   - `ClusterCatalog` - create/manage catalog sources
   - `ClusterExtension` - monitor installed extensions

2. **Management Cluster API** (via in-cluster config) - For reporting status:
   - `ClusterOperator` - report Upgradeable, Available, Progressing conditions

## Environment Variables

The HyperShift deployment adapter sets these environment variables:

- `HOSTED_KUBECONFIG=/etc/openshift/kubeconfig/kubeconfig` - Path to hosted cluster kubeconfig
- `HYPERSHIFT_MODE=true` - Indicates running in HyperShift mode
- Volume mount at `/etc/openshift/kubeconfig/` contains the `admin-kubeconfig` secret

## Required Code Changes

### 1. Client Initialization (`pkg/clients/clients.go`)

#### Current Implementation
```go
func New(cc *controllercmd.ControllerContext) (*Clients, error) {
    kubeClient, err := kubernetes.NewForConfig(cc.ProtoKubeConfig)
    // ... creates all clients from single config
}
```

#### Required Changes
```go
func New(cc *controllercmd.ControllerContext) (*Clients, error) {
    // Management cluster config (in-cluster)
    mgmtConfig := cc.KubeConfig
    
    // Hosted cluster config (from HOSTED_KUBECONFIG if in HyperShift)
    hostedConfig := mgmtConfig
    if hypershiftMode := os.Getenv("HYPERSHIFT_MODE"); hypershiftMode == "true" {
        if hostedKubeconfigPath := os.Getenv("HOSTED_KUBECONFIG"); hostedKubeconfigPath != "" {
            var err error
            hostedConfig, err = clientcmd.BuildConfigFromFlags("", hostedKubeconfigPath)
            if err != nil {
                return nil, fmt.Errorf("failed to load hosted kubeconfig: %w", err)
            }
        }
    }
    
    // Create clients:
    // - Use hostedConfig for: DynamicClient (ClusterCatalog/ClusterExtension)
    // - Use mgmtConfig for: OperatorClient (ClusterOperator status)
    
    dynClient, err := dynamic.NewForConfig(hostedConfig)  // CHANGED
    // ...
    
    operatorClientset, err := operatorclient.NewForConfig(mgmtConfig)  // CHANGED
    // ...
}
```

### 2. ClusterCatalog Client

#### Current Implementation
Creates client from single config.

#### Required Changes
```go
// Use hostedConfig for ClusterCatalog operations
func NewClusterCatalogClient(dynClient dynamic.Interface) *ClusterCatalogClient {
    // dynClient already uses hostedConfig from New()
    // No changes needed here
}
```

### 3. ClusterExtension Client

#### Current Implementation
Creates client from single config.

#### Required Changes
```go
// Use hostedConfig for ClusterExtension operations
func NewClusterExtensionClient(dynClient dynamic.Interface) *ClusterExtensionClient {
    // dynClient already uses hostedConfig from New()
    // No changes needed here
}
```

### 4. Operator Client (ClusterOperator Status)

#### Current Implementation
Reports ClusterOperator status to the same API server as ClusterCatalog.

#### Required Changes
```go
// Use mgmtConfig for ClusterOperator operations
// The OperatorClient is already created with operatorClientset in New()
// which now uses mgmtConfig, so no changes needed here
```

### 5. Default ClusterCatalog Provisioning

The enhancement proposal mentions:

> cluster-olm-operator running in the management cluster creates the default ClusterCatalog in the hosted cluster

This means when the operator starts in HyperShift mode, it should:

```go
func (c *Clients) EnsureDefaultCatalog(ctx context.Context) error {
    if os.Getenv("HYPERSHIFT_MODE") != "true" {
        return nil // Only create in HyperShift mode
    }
    
    // Create default ClusterCatalog using the hosted cluster DynamicClient
    // This ensures the catalog is created in the hosted cluster, not management
    catalogGVR := catalogdv1.GroupVersion.WithResource("clustercatalogs")
    
    catalog := &unstructured.Unstructured{
        Object: map[string]interface{}{
            "apiVersion": "olm.operatorframework.io/v1",
            "kind":       "ClusterCatalog",
            "metadata": map[string]interface{}{
                "name": "openshift-catalog",
            },
            "spec": map[string]interface{}{
                // Default catalog configuration
            },
        },
    }
    
    _, err := c.DynamicClient.Resource(catalogGVR).Create(ctx, catalog, metav1.CreateOptions{})
    if err != nil && !apierrors.IsAlreadyExists(err) {
        return fmt.Errorf("failed to create default catalog: %w", err)
    }
    return nil
}
```

## Testing Strategy

### Unit Tests
1. Test client initialization with `HYPERSHIFT_MODE=true`
2. Verify correct config used for each client type
3. Test default catalog provisioning logic

### Integration Tests
1. Deploy cluster-olm-operator in HyperShift environment
2. Verify ClusterCatalog created in hosted cluster
3. Verify ClusterOperator status reported to management cluster
4. Verify ClusterExtension monitoring works across API servers

## Migration Path

1. **Phase 1**: Implement dual-client support with feature flag
   - Add `HYPERSHIFT_MODE` detection
   - Create dual configs when flag is set
   - Maintain backward compatibility

2. **Phase 2**: Test in dev/staging HyperShift clusters
   - Verify ClusterCatalog operations in hosted cluster
   - Verify ClusterOperator reporting in management cluster

3. **Phase 3**: Production rollout
   - HyperShift deployment sets environment variables
   - Operator automatically uses dual-API mode

## Open Questions

1. **ClusterOperator Resource Location**: Should the ClusterOperator resource exist in:
   - Management cluster only? (current assumption)
   - Both clusters?
   
2. **Error Handling**: How should the operator handle scenarios where:
   - Hosted cluster API is unavailable?
   - Management cluster API is unavailable?
   
3. **Leader Election**: Should leader election happen:
   - In management cluster?
   - In hosted cluster?
   - Both?

## References

- Enhancement Proposal: `~/devel/openshift/enhancements/enhancements/olm/olmv1-hypershift-support.md`
- HyperShift Component: `~/devel/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olmv1/clusterolmoperator/`
- Shared Kubeconfig Utility: `~/devel/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olmv1/util/util.go`
