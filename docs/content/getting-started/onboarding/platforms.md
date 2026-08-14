# Supported Cloud Platforms

> **See also**: [Multi-Platform Support](../../reference/multi-platform-support.md) for the full support matrix across HostedCluster, NodePool, and management cluster platform combinations.

HyperShift supports multiple infrastructure platforms. Each platform implements the same `Platform` interface but brings its own CAPI provider, credential model, and networking primitives. Some platforms are generally available while others are behind feature gates.

```mermaid
graph TD
    subgraph "Cloud Providers"
        subgraph "AWS"
            AWS_SM["AWS Self-Managed"]
            AWS_M["AWS Managed<br/><i>ROSA HCP</i>"]
        end

        subgraph "Azure"
            AZ_SM["Azure Self-Managed"]
            AZ_M["Azure Managed<br/><i>ARO HCP</i>"]
        end

        subgraph "GCP"
            GCP_SM["GCP Self-Managed<br/><i>Feature-gated</i>"]
            GCP_M["GCP Managed<br/><i>GCP HCP</i>"]
        end
    end

    subgraph "Virtualization"
        KV["KubeVirt<br/><i>VMs on K8s</i>"]
    end

    subgraph "Bare Metal"
        AG["Agent<br/><i>Bare-metal</i>"]
    end

    subgraph "Other"
        OS["OpenStack<br/><i>Feature-gated</i>"]
        IBM["IBM Cloud<br/><i>PowerVS</i>"]
    end

    style AWS_SM fill:#f96,stroke:#333
    style AWS_M fill:#f96,stroke:#333
    style AZ_SM fill:#69f,stroke:#333
    style AZ_M fill:#69f,stroke:#333
    style GCP_SM fill:#6c6,stroke:#333
    style GCP_M fill:#6c6,stroke:#333
    style KV fill:#c6f,stroke:#333
    style AG fill:#a65,stroke:#333
    style OS fill:#f66,stroke:#333
    style IBM fill:#999,stroke:#333
```

---

## Platform Interface

Every platform must implement the `Platform` interface defined in `hypershift-operator/controllers/hostedcluster/internal/platform/platform.go`. The HyperShift Operator uses this interface to abstract away cloud-specific details during HostedCluster reconciliation.

```go
type Platform interface {
    // ReconcileCAPIInfraCR creates/updates the platform-specific CAPI infrastructure CR
    // that will be referenced by the CAPI Cluster CR.
    ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
        hcluster *hyperv1.HostedCluster, controlPlaneNamespace string,
        apiEndpoint hyperv1.APIEndpoint) (client.Object, error)

    // CAPIProviderDeploymentSpec returns the DeploymentSpec for the CAPI provider
    // with platform-specific volumes, secrets, and containers.
    CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster,
        hcp *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error)

    // ReconcileCredentials copies cloud credentials from the HostedCluster namespace
    // into the control plane namespace for the CPO to consume.
    ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
        hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error

    // ReconcileSecretEncryption copies KMS-related resources into the control plane
    // namespace (if the platform supports KMS).
    ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
        hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error

    // CAPIProviderPolicyRules returns additional RBAC PolicyRules required by the
    // CAPI provider to manage platform resources. Return nil if none are needed.
    CAPIProviderPolicyRules() []rbacv1.PolicyRule

    // DeleteCredentials cleans up platform credential resources so they don't leak
    // when a HostedCluster is deleted.
    DeleteCredentials(ctx context.Context, c client.Client,
        hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error
}
```

The `GetPlatform()` function in the same file uses a `switch` on `hcluster.Spec.Platform.Type` to instantiate the correct implementation (AWS, Azure, GCP, KubeVirt, Agent, etc.).

---

## Platform Comparison

| Aspect | AWS (Self-Managed) | AWS (ROSA HCP) | Azure (Self-Managed) | Azure (ARO HCP) | GCP (GCP HCP) | KubeVirt | Agent |
|--------|-------------------|----------------|---------------------|-----------------|---------------|----------|-------|
| **Managed service** | No | Yes | No | Yes | Yes | No | No |
| **CAPI Provider** | CAPA | CAPA | CAPZ | CAPZ | CAPG | - | - |
| **Identity** | STS / IRSA | STS / IRSA | Workload Identity | Workload Identity | Workload Identity | N/A | N/A |
| **KMS** | AWS KMS | AWS KMS | Azure Key Vault | Azure Key Vault | GCP KMS | N/A | N/A |
| **Private connectivity** | AWS PrivateLink | AWS PrivateLink | Azure Private Link Service | Azure Private Link Service | Private Service Connect | N/A | N/A |
| **Infra provisioning** | EC2/VPC | EC2/VPC | VMs/VNet | VMs/VNet | GCE/VPC | KubeVirt VMs | BareMetalHost |
| **Cloud Controller Manager** | aws-ccm | aws-ccm | azure-ccm | azure-ccm | gcp-ccm | kubevirt-ccm | N/A |

!!! tip "Explore yourself"
    Browse the platform implementation directories to see how each platform implements the interface:

    - `hypershift-operator/controllers/hostedcluster/internal/platform/aws/`
    - `hypershift-operator/controllers/hostedcluster/internal/platform/azure/`
    - `hypershift-operator/controllers/hostedcluster/internal/platform/gcp/`
    - `hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt/`
    - `hypershift-operator/controllers/hostedcluster/internal/platform/agent/`
    - `hypershift-operator/controllers/hostedcluster/internal/platform/openstack/`
    - `hypershift-operator/controllers/hostedcluster/internal/platform/powervs/`

---

## AWS Infrastructure

AWS is the most mature platform. Both self-managed and managed (ROSA HCP) deployments use the same underlying infrastructure primitives.

```mermaid
graph TD
    subgraph "Management Cluster VPC"
        HO[HyperShift Operator]

        subgraph "CP Namespace"
            KAS[kube-apiserver]
            NLB[Network Load Balancer<br/><i>API endpoint</i>]
            CAPA[CAPA Controller]
        end
    end

    subgraph "Guest Cluster VPC"
        subgraph "Public Subnets"
            IGW[Internet Gateway]
            NATGW[NAT Gateway]
        end
        subgraph "Private Subnets"
            W1[Worker EC2<br/>Subnet AZ-a]
            W2[Worker EC2<br/>Subnet AZ-b]
            W3[Worker EC2<br/>Subnet AZ-c]
        end
    end

    HO -->|creates| NLB
    NLB --> KAS
    CAPA -->|provisions| W1
    CAPA -->|provisions| W2
    CAPA -->|provisions| W3
    W1 & W2 & W3 -->|egress via| NATGW
    NATGW --> IGW

    style NLB fill:#f96,stroke:#333
    style KAS fill:#69f,stroke:#333
```

The guest cluster VPC contains public subnets (with an Internet Gateway and NAT Gateway for outbound traffic) and private subnets where worker EC2 instances run. The CAPI AWS provider (CAPA) manages the machine lifecycle. The kube-apiserver is fronted by a Network Load Balancer for external access.

---

## AWS PrivateLink

For private clusters on AWS, HyperShift uses AWS PrivateLink to expose the kube-apiserver without traversing the public internet. The CPO runs a dedicated controller that manages the PrivateLink endpoint service and VPC endpoint:

- **Source**: `control-plane-operator/controllers/awsprivatelink/`
- The management cluster creates a VPC Endpoint Service backed by the kube-apiserver NLB
- Consumers create a VPC Endpoint in their VPC that connects through PrivateLink
- DNS is configured so the API server hostname resolves to the private endpoint

This model is critical for ROSA HCP where the control plane runs in a service account VPC that the customer never sees directly.

---

## KubeVirt Nested Virtualization

KubeVirt is unique because it runs guest cluster worker nodes as virtual machines on an existing Kubernetes cluster, creating a nested architecture:

```mermaid
graph TD
    subgraph "Infrastructure Cluster (OCP with KubeVirt)"
        KV_OP[KubeVirt Operator]
        subgraph "Management Cluster Layer"
            HO[HyperShift Operator]
            subgraph "CP Namespace"
                CPO_KV[CPO]
                KAS_KV[kube-apiserver]
            end
        end

        subgraph "KubeVirt VMs"
            VM1[VM: Worker 1<br/><i>runs as a Pod</i>]
            VM2[VM: Worker 2<br/><i>runs as a Pod</i>]
            VM3[VM: Worker N<br/><i>runs as a Pod</i>]
        end
    end

    KV_OP -->|manages VMs| VM1 & VM2 & VM3
    HO -->|creates HC| CPO_KV
    CPO_KV -->|manages| KAS_KV
    KAS_KV -.->|konnectivity| VM1 & VM2 & VM3

    style VM1 fill:#c6f,stroke:#333
    style VM2 fill:#c6f,stroke:#333
    style VM3 fill:#c6f,stroke:#333
```

Key differences from cloud platforms:

- **No CAPI provider**: KubeVirt manages VMs directly through the KubeVirt API
- **No cloud credentials**: The infrastructure cluster already has everything needed
- **Nested networking**: Guest cluster pods run inside VMs that run inside pods on the infra cluster
- **Shared infrastructure**: The management cluster, control plane, and worker VMs all share the same underlying cluster

---

## Cloud Controller Managers

Each cloud platform has a Cloud Controller Manager (CCM) that runs in the control plane namespace. The CCM is responsible for:

- Initializing nodes with cloud-specific metadata (zone, instance type, addresses)
- Managing cloud load balancers for Services of type `LoadBalancer`
- Handling node lifecycle (detecting when cloud instances are terminated)

Platform-specific CCM implementations are registered as CPO v2 components:

- `control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/aws/`
- `control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/azure/`
- `control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/gcp/`
- `control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/kubevirt/`
- `control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/openstack/`
- `control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/powervs/`

Each CCM component adapts a `cloud-provider-<platform>` binary with the appropriate cloud configuration file, credentials, and feature gates for the hosted cluster.

---

## Credential Management Pattern

Cloud credentials flow from the user-facing HostedCluster resource into the control plane namespace where they are consumed by the CAPI provider, CCM, and other platform-aware components:

```mermaid
sequenceDiagram
    participant User
    participant HC as HostedCluster
    participant HO as HyperShift Operator
    participant CPN as CP Namespace
    participant CAPI as CAPI Provider
    participant CCM as Cloud Controller Manager

    User->>HC: Creates HC with cloud credentials<br/>(Secret references in spec.platform)
    HO->>HO: platform.ReconcileCredentials()
    HO->>CPN: Copies credential Secrets<br/>into control plane namespace
    HO->>HO: platform.ReconcileSecretEncryption()
    HO->>CPN: Copies KMS Secrets<br/>(if platform supports KMS)
    CPN->>CAPI: Mounts credentials as volumes
    CPN->>CCM: Mounts cloud config + credentials
```

The cloud configuration file that the CCM and other components consume is generated per-platform:

- **AWS**: `control-plane-operator/controllers/hostedcontrolplane/cloud/aws/providerconfig.go`
- **Azure**: `control-plane-operator/controllers/hostedcontrolplane/cloud/azure/providerconfig.go`
- **OpenStack**: `control-plane-operator/controllers/hostedcontrolplane/cloud/openstack/providerconfig.go`

These files build the `cloud.conf` (or equivalent) that gets mounted into the CCM, KCM, and other components that need to interact with the cloud API.
