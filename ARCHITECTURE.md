# Architecture

HyperShift is middleware for hosting OpenShift control planes at scale. It decouples the control plane from the data plane so that control plane components run as pods on a management cluster while worker nodes run in separate infrastructure. This provides cost-efficient, fast provisioning with strong separation of concerns and portability across clouds.

## Key Concepts

- **Management Cluster** — an OpenShift cluster that hosts HyperShift and zero to many hosted clusters.
- **Hosted Cluster** — an OpenShift API endpoint managed by HyperShift. Represented by a `HostedCluster` custom resource.
- **Hosted Control Plane** — the control plane components (etcd, kube-apiserver, controller-manager, etc.) running as pods in a dedicated namespace on the management cluster. Represented by a `HostedControlPlane` custom resource.
- **NodePool** — a set of compute worker nodes in the hosted cluster's infrastructure. Represented by a `NodePool` custom resource.

See [Concepts and Personas](docs/content/reference/concepts-and-personas.md) for the full glossary.

## Core Components

| Component | Directory | Role |
|-----------|-----------|------|
| **HyperShift Operator** | `hypershift-operator/` | Singleton on the management cluster. Reconciles `HostedCluster` and `NodePool` resources, creates control plane namespaces, deploys the CPO and CAPI controllers. |
| **Control Plane Operator (CPO)** | `control-plane-operator/` | One instance per hosted cluster. Reconciles the `HostedControlPlane` and manages ~40 control plane components (etcd, kube-apiserver, etc.) using the CPOv2 declarative framework. |
| **Control Plane PKI Operator** | `control-plane-pki-operator/` | Manages certificate rotation, CSR signing, and break-glass credential access for each hosted cluster. |
| **Karpenter Operator** | `karpenter-operator/` | Manages Karpenter resources for node auto-scaling. |
| **Ignition Server** | `ignition-server/` | HTTPS server that generates and serves ignition configs to worker nodes during bootstrap. |

## Design Invariants

- Communication between management cluster and hosted cluster is **unidirectional**. A hosted cluster has no awareness of the management cluster.
- Communication between management cluster and hosted cluster is only allowed **from within each control plane namespace**.
- Worker nodes run **only user workloads**. A hosted cluster does not expose CRDs, CRs, or pods that let users manipulate HyperShift-owned features.
- HyperShift components **do not own or manage user infrastructure platform credentials**.

See [Goals and Design Invariants](docs/content/reference/goals-and-design-invariants.md) for the full set including control plane and data plane ingress separation.

## Platform Support

- AWS — self-managed and managed (ROSA HCP)
- Azure — self-managed and managed (ARO HCP)
- GCP
- IBM Cloud (PowerVS)
- KubeVirt
- OpenStack
- Agent

## Further Reading

- [Architecture and Main Components](docs/content/getting-started/onboarding/architecture.md) — detailed component walkthrough with Mermaid diagrams
- [Controller Architecture](docs/content/reference/controller-architecture.md) — controller diagrams, reconcile flows, and resource dependency graphs
- [Goals and Design Invariants](docs/content/reference/goals-and-design-invariants.md) — security and architectural invariants
- [Concepts and Personas](docs/content/reference/concepts-and-personas.md) — terminology and user personas
- [Versioning and Support](docs/content/reference/versioning-support.md) — release cycle, version skew policies, and support matrices
- [Platform Architecture](docs/content/reference/architecture/) — platform-specific details (PrivateLink, managed Azure identity, shared ingress)
