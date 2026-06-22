# Control Plane Component Framework

This library provides the reconciliation framework for managing individual control plane workloads (Deployments, StatefulSets, Jobs, CronJobs) and their associated manifests (ConfigMaps, Secrets, RBAC, Services, etc.) within HyperShift's hosted control plane.

## Primary Consumer

The **Control Plane Operator** (CPO) is the main consumer. Each control plane component (KAS, etcd, OAuth, KCM, scheduler, DNS, cloud controllers, etc.) is implemented as a `ControlPlaneComponent` in `control-plane-operator/controllers/hostedcontrolplane/v2/`. Manifests live alongside each component in `v2/assets/{component-name}/` as embedded YAML.

## CR Representation

Every component gets a `ControlPlaneComponent` CR (`hypershift.openshift.io/v1beta1`) in the same namespace as the `HostedControlPlane`. The CR tracks:

- **Conditions**: `Available` (component is ready) and `RolloutComplete` (workload version matches desired)
- **Version**: Current deployed version
- **Resources**: List of resources managed by the component

The framework reconciles these CRs automatically. Components declare dependencies on other components; reconciliation waits for dependencies to be `Available` and `RolloutComplete` at the correct version before proceeding.

## Reuse Beyond CPO

The framework is also consumed by:

- **Karpenter Operator**: Manages the Karpenter component using the same `ControlPlaneComponent` and `ControlPlaneContext` abstractions
- **HyperShift Operator**: Uses constants and context types from this package

Ideally, other reconciliation loops like HCCO would also reuse these primitives rather than implementing parallel patterns for resource reconciliation, status tracking, and dependency management. The `ControlPlaneContext`, `WorkloadContext`, and builder API are designed to be general-purpose.

## Key Files

| File | Purpose |
|------|---------|
| `controlplane-component.go` | Core interfaces: `ControlPlaneComponent`, `ComponentOptions`, `ControlPlaneContext` |
| `builder.go` | Fluent builder API for constructing components |
| `deployment.go`, `statefulset.go`, `job.go`, `cronjob.go` | Workload-type providers |
| `status.go` | Status reconciliation: dependency checks, Available/RolloutComplete conditions |
| `defaults.go` | Default container configs: resource limits, security context, probes |
| `generic-adapter.go` | Applies adapt functions and predicates to individual manifests |
| `konnectivity-container.go` | Konnectivity proxy sidecar injection |
| `token-minter-container.go` | Token-minter sidecar for cloud/KAS access |
| `common.go` | Helpers: `AdaptPodDisruptionBudget`, `DisableIfAnnotationExist`, `EnableForPlatform` |
