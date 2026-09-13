# Cluster Lifecycle and Control Plane

---

## HostedCluster Lifecycle

### Creation

```mermaid
sequenceDiagram
    participant User
    participant HC as HostedCluster
    participant HO as HyperShift Operator
    participant CPNS as CP Namespace
    participant CPO as Control Plane Operator
    participant CAPI as ClusterAPI
    participant NP as NodePool
    participant Cloud as Cloud Provider

    User->>HC: Creates HostedCluster CR
    HO->>HC: Watch & Reconcile
    HO->>HO: Validates release image, config
    HO->>CPNS: Creates namespace<br/>{hc-ns}-{hc-name}
    HO->>CPNS: Copies secrets<br/>(pull-secret, SSH, encryption)
    HO->>CPNS: Creates HostedControlPlane CR
    HO->>CPNS: Deploys CPO Deployment
    HO->>CPNS: Deploys CAPI Manager + Provider
    HO->>CPNS: Configures NetworkPolicies

    CPO->>CPO: Reads HCP, resolves release image
    CPO->>CPNS: Deploys etcd StatefulSet
    CPO->>CPNS: Deploys kube-apiserver
    CPO->>CPNS: Deploys +30 components
    CPO->>HC: Reports status via HCP

    HO->>HC: Copies kubeconfig to user namespace

    User->>NP: Creates NodePool CR
    HO->>NP: Watch & Reconcile
    HO->>CPNS: Creates Token + UserData Secrets
    HO->>CAPI: Creates MachineDeployment + MachineTemplate
    CAPI->>Cloud: Provisions instances
    Cloud-->>CPNS: Nodes boot, fetch ignition
    Cloud-->>HC: Nodes join the guest cluster
```

!!! tip "Explore yourself"
    Follow the creation flow step by step in `hostedcluster_controller.go`:

    1. `Reconcile()` (~line 337) - entry point
    2. `reconcileHostedControlPlane()` (~line 2404) - HCP creation
    3. `reconcileControlPlaneOperator()` - CPO deployment
    4. `reconcileCAPIManager()` - CAPI deployment
    5. Network policies: `hypershift-operator/controllers/hostedcluster/network_policies.go`

### Steady State

- The CPO continuously reconciles all components against the HCP spec
- The PKI operator rotates certificates automatically
- The NodePool controller manages auto-repair and scaling
- Status flow: `CPO -> HCP status -> HO -> HC status`

### Upgrade

> **See also**: [Upgrades](../../how-to/upgrades.md) for detailed upgrade procedures and version skew policies.

Control plane and data plane upgrades are **decoupled**:

```mermaid
graph LR
    subgraph "Control Plane Upgrade"
        A[Change HC.spec.release] --> B[HO updates HCP]
        B --> C[CPO deploys new versions<br/>of components]
    end

    subgraph "Data Plane Upgrade"
        D[Change NP.spec.release] --> E[New config hash]
        E --> F[New token/userdata Secrets]
        F --> G[CAPI rolling update<br/>of MachineDeployment]
    end

    A -.->|independent| D
```

- `controlPlaneRelease`: allows patching management-side components without touching the data plane
- NodePool releases can be updated independently (within N-3 y-stream skew)

### Deletion

```mermaid
sequenceDiagram
    participant User
    participant HO as HyperShift Operator
    participant CAPI as ClusterAPI
    participant Cloud as Cloud Provider

    User->>HO: Delete HostedCluster
    HO->>CAPI: Deletes Machines, MachineDeployments
    CAPI->>Cloud: Terminates instances
    HO->>HO: Deletes HCP, waits for CPO cleanup
    HO->>Cloud: Cleans up cloud resources<br/>(LBs, DNS, SGs, endpoints)
    HO->>HO: Cleans up cluster-wide RBAC
    HO->>HO: Removes finalizer
    Note over HO: Supports grace period via<br/>hypershift.openshift.io/destroy-grace-period
```

!!! tip "Explore yourself"
    The deletion flow starts at `r.delete()` (~line 501 in `hostedcluster_controller.go`). Notice the `CloudResourcesDestroyed` and `HostedClusterDestroyed` conditions.

---

## Control Plane in Detail

### CPO Reconciliation Flow

> **See also**: [Controller Architecture](../../reference/controller-architecture.md) for the full controller dependency graph and reconciliation details.

```mermaid
flowchart TD
    START[Reconcile HCP] --> FETCH[Fetch HostedControlPlane]
    FETCH --> DEL{Deletion?}
    DEL -->|Yes| CLEANUP[Cleanup cloud resources<br/>Remove finalizer]
    DEL -->|No| FIN[Add finalizer]
    FIN --> VALIDATE[Validate configuration]
    VALIDATE --> ETCD_STATUS[Check etcd status]
    ETCD_STATUS --> KMS[Validate KMS config]
    KMS --> KAS_STATUS[Check KAS availability]
    KAS_STATUS --> INFRA[Setup infrastructure status<br/>endpoints, DNS, routes]
    INFRA --> IGNITION_CFG[Reconcile ignition configs]
    IGNITION_CFG --> OAUTH_PWD[Reconcile kubeadmin password]
    OAUTH_PWD --> CONTEXT[Build ControlPlaneContext]
    CONTEXT --> COMPONENTS[Iterate registered components]

    subgraph "Component Loop"
        COMPONENTS --> C1[Component.Reconcile]
        C1 --> PRED{Predicate<br/>passes?}
        PRED -->|No| DELETE_RES[Delete component resources]
        PRED -->|Yes| DEPS{Dependencies<br/>ready?}
        DEPS -->|No| WAIT[Skip, requeue]
        DEPS -->|Yes| RECONCILE[Reconcile manifests<br/>+ workload]
        RECONCILE --> STATUS_CR[Update ControlPlaneComponent CR]
    end
```

!!! tip "Explore yourself"
    In `hostedcontrolplane_controller.go`, the component iteration loop is at ~line 1232:

    ```go
    for _, c := range r.components {
        r.Log.Info("Reconciling component", "component_name", c.Name())
        if err := c.Reconcile(cpContext); err != nil {
            errs = append(errs, err)
        }
    }
    ```

### Component Dependencies

```mermaid
graph TD
    PKI[PKI Operator] --> |no deps| READY1((Ready))
    ETCD[etcd] --> |no deps| READY2((Ready))
    FG[FeatureGate Generator] --> |no deps| READY3((Ready))
    ROUTER[Router] --> |no deps| READY4((Ready))

    READY2 --> KAS[kube-apiserver]
    READY3 --> KAS

    KAS --> |implicit dependency| KCM[kube-controller-manager]
    KAS --> |implicit dependency| SCHED[kube-scheduler]
    KAS --> |implicit dependency| OAPI[openshift-apiserver]
    KAS --> |implicit dependency| CVO[CVO]
    KAS --> |implicit dependency| ALL["All other<br/>components (~30)"]

    style PKI fill:#c8e6c9
    style ETCD fill:#c8e6c9
    style FG fill:#c8e6c9
    style ROUTER fill:#c8e6c9
    style KAS fill:#ffcc80
```

KAS is an implicit dependency for all components **except**: etcd, featuregate-generator, control-plane-operator, cluster-api, capi-provider, karpenter, and router.

### Status Propagation

```mermaid
graph LR
    CPC[ControlPlaneComponent CRs<br/><i>per-component</i><br/>Available / RolloutComplete] --> HCP_STATUS[HCP Status<br/>Conditions:<br/>EtcdAvailable<br/>KubeAPIServerAvailable<br/>ValidConfiguration]
    HCP_STATUS --> HC_STATUS[HC Status<br/>Conditions:<br/>Available<br/>Progressing<br/>Degraded]

    HC_STATUS --> USER[Visible to the user]
```

!!! tip "Explore yourself"
    - HC conditions are defined in `api/hypershift/v1beta1/hostedcluster_conditions.go`
    - NP conditions are in `api/hypershift/v1beta1/nodepool_conditions.go`
    - The CPOv2 status logic is in `support/controlplane-component/status.go`
