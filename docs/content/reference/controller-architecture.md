# Controller architecture

## High Level Overview

### Physical layout and operating model

Legend:

- Yellow box: namespace
- Rounded box: processes
- Rectangle: CR instances

```mermaid
flowchart LR
  subgraph hypershift
    cluster-operator([HyperShift Operator])
  end

  subgraph user-clusters
    HostedClusterA
    NodePoolA
  end

  subgraph cluster-a
    control-plane-operator([Control Plane Operator])

    capi-manager([CAPI Manager])
    capi-provider([CAPI Provider])

    HostedControlPlane
    ExternalInfraCluster

    cp-components([Control Plane Components])

    capi-cluster[CAPICluster]

    capi-machine-template[CAPIInfrastructureMachineTemplate]
    capi-machineset[CAPI MachineSet]
    capi-machine[CAPI Machine]
    capi-provider-machine[CAPIInfrastructureMachine]
  end

  cluster-operator-->|reconciles|HostedClusterA
  cluster-operator-->|operates|control-plane-operator
  cluster-operator-->|operates|capi-manager
  cluster-operator-->|operates|capi-provider
  cluster-operator-->|creates|HostedControlPlane
  cluster-operator-->|creates|capi-cluster
  cluster-operator-->|creates|ExternalInfraCluster
  cluster-operator-->|reconciles|NodePoolA
  cluster-operator-->|creates|capi-machine-template
  cluster-operator-->|creates|capi-machineset

  control-plane-operator-->|operates|cp-components

  control-plane-operator-->|reconciles|HostedControlPlane

  capi-manager-->|reconciles|capi-cluster
  capi-manager-->|reconciles|capi-machineset
  capi-manager-->|creates|capi-machine

  capi-provider-->|reconciles|capi-machine
  capi-provider-->|creates|capi-provider-machine
```

TODO:
1. How do we (or should we) represent an input/output or "consumes" relationship (e.g. the hypershift operator creates and syncs machine templates, and the CAPI provider _reads_ the template, but nothing actively watches templates and does work in reaction to them directly)



## Major Components

### HyperShift Operator

The **HyperShift Operator** is a singleton within the management cluster that manages the lifecycle of hosted clusters represented by `HostedCluster` resources.

A single version of the the HyperShift Operator knows how to manage multiple hosted OCP versions.

The HyperShift Operator is responsible for:

- Processing `HostedCluster` and `NodePool` resources and managing Control Plane Operator and [Cluster API (CAPI)](https://github.com/kubernetes-sigs/cluster-api) deployments which do the actual work of installing a control plane.
- Managing the lifecycle of the hosted cluster by handling rollouts of new Control Plane Operator and CAPI deployments based on version changes to `HostedCluster` and `NodePool` resources.
- Aggregating and surfacing information about clusters.


#### HostedCluster Controller

```mermaid

graph TD
  hosted-cluster-controller[HostedCluster Controller] --> reconcile([Reconcile HostedCluster])
  reconcile --> is-deleted{{Deleted?}}
  is-deleted -->|Yes| teardown([Teardown])
  is-deleted -->|No| sync([Sync])
  
  teardown -->teardown-complete{{Teardown complete?}}
  teardown-complete -->|Yes| return
  teardown-complete -->|No| reconcile
  
  sync --> create-namespace([Create Namespace])
  create-namespace --> deploy-cp-operator([Deploy Control Plane Operator])
  deploy-cp-operator --> deploy-capi-manager([Deploy CAPI Manager])
  deploy-capi-manager --> deploy-capi-provider([Deploy CAPI Provider])
  deploy-capi-provider --> create-capi-cluster([Create CAPICluster])
  create-capi-cluster --> create-hosted-control-plane([Create HostedControlPlane])
  create-hosted-control-plane --> create-external-infra-cluster([Create ExternalInfraCluster])
  create-external-infra-cluster -->has-initial-nodes{{HostedCluster has initial nodes?}}
  has-initial-nodes -->|Yes| create-node-pool([Create NodePool])
  has-initial-nodes -->|No| return
  create-node-pool --> return
  
  return([End])
  
```

#### NodePool Controller

```mermaid
graph TD
  nodepool-controller[NodePool Controller] --> reconcile([Reconcile NodePool])
  reconcile --> is-deleted{{Deleted?}}
  is-deleted -->|Yes| teardown([Teardown])
  is-deleted -->|No| sync([Sync])
  
  sync --> create-capi-machineset([Create CAPIMachineSet])
  create-capi-machineset --> create-capi-infra-machine-template([Create CAPIInfrastructureMachineTemplate])
  
  create-capi-infra-machine-template --> return
  
  teardown -->teardown-complete{{Teardown complete?}}
  teardown-complete -->|Yes| return
  teardown-complete -->|No| reconcile
  
  return([End])
```

#### ExternalInfraCluster Controller

```mermaid

graph TD
  external-infra-cluster-controller[ExternalInfraCluster Controller] --> reconcile([Reconcile ExternalInfraCluster])
  reconcile --> is-deleted{{Deleted?}}
  is-deleted -->|Yes| teardown([Teardown])
  is-deleted -->|No| sync([Sync])
  
  teardown -->teardown-complete{{Teardown complete?}}
  teardown-complete -->|Yes| return
  teardown-complete -->|No| reconcile
  
  sync --> get-hosted-control-plane([Get HostedControlPlane])
  get-hosted-control-plane -->is-hcp-ready{{Is HostedControlPlane ready?}}
  is-hcp-ready -->|No| reconcile
  is-hcp-ready -->|Yes| update-infra-status([Update ExternalInfraCluster status])
  update-infra-status --> return
  
  return([End])
  
```

### Control Plane Operator

The **Control Plane Operator** is a deployed by the HyperShift Operator into a hosted control plane namespace and manages the rollout of a single version of the the hosted cluster's control plane.

The Control Plane Operator is versioned in lockstep with a specific OCP version and is decoupled from the management cluster's version.

The Control Plane Operator is responsible for:

- Provisioning all the infrastructure required to host a control plane (whether this means creating or adopting existing infrastructure). This infrastructure may be management cluster resources, external cloud provider resources, etc.
- Deploying an OCP control plane configured to run in the context of the provisioned infrastructure.
- Implementing any versioned behavior necessary to rollout the new version (e.g. version specific changes at layers above OCP itself, like configuration or infrastructure changes).


#### HostedControlPlane Controller

```mermaid

graph TD
  hosted-control-plane-controller[HostedControlPlane Controller] --> reconcile([Reconcile HostedControlPlane])
  reconcile --> is-deleted{{Deleted?}}
  is-deleted -->|Yes| teardown([Teardown])
  is-deleted -->|No| sync([Sync])
  
  teardown -->teardown-complete{{Teardown complete?}}
  teardown-complete -->|Yes| return
  teardown-complete -->|No| reconcile
  
  sync --> create-infra([Deploy Control Plane<br>Components])
  create-infra --> create-config-operator([Deploy Hosted Cluster<br>Config Operator])
  create-config-operator -->is-infra-ready{{Infra ready?}}
  is-infra-ready -->|Yes| update-hosted-controlplane-ready([Update HostedControlPlane status])
  is-infra-ready -->|No| reconcile
  update-hosted-controlplane-ready --> return
  
  return([End])
  
```

### Hosted Cluster Config Operator

The **Hosted Cluster Config Operator** is a control plane component maintained by HyperShift that's a peer to other control plane components (e.g., etcd, apiserver, controller-manager), and is managed by the Control Plane Operator in the same way as those other control plane components.

The Hosted Cluster Config Operator is versioned in lockstep with a specific OCP version and is decoupled from the management cluster's version.

The Hosted Cluster Config Operator is responsible for:

- Reading CAs from the hosted cluster to configure the kube controller manager CA bundle running in the hosted control plane
- Reconciling resources that live on the hosted cluster:
    * CRDs created by operators that are absent from the hosted cluster (RequestCount CRD created by cluster-kube-apiserver-operator)
    * Clearing any user changes to the ClusterVersion resource (all updates should be driven via HostedCluster API)
    * ClusterOperator stubs for control plane components that run outside.
    * Global Configuration that is managed via the HostedCluster API
    * Namespaces that are normally created by operators that are absent from the cluster.
    * RBAC that is normally created by operators that are absent from the cluster.
    * Registry configuration
    * Default ingress controller
    * Control Plane PKI (kubelet serving CA, control plane signer CA)
    * Konnectivity Agent
    * OpenShift APIServer resources (APIServices, Service, Endpoints)
    * OpenShift OAuth APIServer resources (APIServices, Service, Endpoints)
    * Monitoring Configuration (set node selector to non-master nodes)
    * Pull Secret
    * OAuth serving cert CA
    * OAuthClients required by the console
    * Cloud Credential Secrets (contain STS role for components that need cloud access)
    * OLM CatalogSources
    * OLM PackageServer resources (APIService, Service, Endpoints)

## Resource dependency diagram

* Dotted lines are dependencies (ownerRefs)
* Solid lines are associations (e.g. infrastructureRefs or controlPlaneRefs on specs)

```mermaid
classDiagram
  HostedCluster
  HostedControlPlane ..> CAPICluster
  ExternalInfraCluster ..> CAPICluster
  CAPICluster ..> HostedCluster
  CAPICluster --> HostedControlPlane
  CAPICluster --> ExternalInfraCluster
  CAPIMachineSet ..> CAPICluster
  CAPIMachineSet --> CAPIInfrastructureMachineTemplate
  CAPIMachine ..>CAPIMachineSet
  CAPIMachine -->CAPIInfrastructureMachine
  CAPIInfrastructureMachine ..>CAPIMachine
  CAPIInfrastructureMachineTemplate ..>CAPICluster
```

## Transformations

Trying to show how certain important resources are derived from others. These are resources created by our operators, not by CAPI.

```mermaid
classDiagram
  CAPICluster ..> HostedControlPlane
  CAPICluster ..> ExternalInfraCluster
  HostedControlPlane ..> HostedCluster
  ExternalInfraCluster ..> HostedCluster

```

```mermaid
classDiagram
  CAPIInfrastructureTemplate ..> NodePool
  CAPIInfrastructureTemplate ..> HostedCluster
  CAPIMachineSet ..> NodePool
  CAPIMachineSet ..> HostedCluster
  CAPIMachineSet ..> CAPIInfrastructureTemplate
```

