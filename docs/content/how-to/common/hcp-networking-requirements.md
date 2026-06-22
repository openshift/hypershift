# HCP Networking Requirements

This document outlines the networking port requirements for Hosted Control Plane (HCP) deployments. Understanding these requirements is essential for properly configuring firewalls, security groups, and network policies.

## Overview

Hosted Control Planes require specific network ports to be open for communication between the management cluster, hosted control plane components, and worker nodes. The ports are categorized into ingress (incoming traffic to HCP) and egress (outgoing traffic from HCP) requirements.

!!! note "Networking Perspective"
    All port requirements in this document are from the perspective of the Hosted Control Plane (HCP) namespace. Ingress means traffic entering the HCP namespace, and egress means traffic leaving the HCP namespace.

## Common Ingress Ports

The following port is required for incoming traffic to the Hosted Control Plane across all platforms:

| Port | Protocol | Service | Description | Code Reference |
|------|----------|---------|-------------|----------------|
| 6443 | TCP | Kubernetes API Server | Primary API server port for kubectl and cluster communication | `support/config/constants.go:35` - `KASSVCPort = 6443` |

!!! note "Service Publishing Strategy Determines Additional Ports"
    Ignition and Konnectivity services are exposed via the configured service publishing strategy:

    - **Route** (default on OpenShift): Traffic flows through the OpenShift router on port **443**. No additional firewall rules needed beyond standard HTTPS.
    - **NodePort**: Direct access required to **port 8091** (Konnectivity) and **port 8443** (Ignition Proxy). See Agent and None platform sections.
    - **LoadBalancer**: Direct access required to **port 8091** (Konnectivity) via the cloud load balancer.

## Common Egress Ports

The following ports must be accessible for outgoing traffic from the Hosted Control Plane across all platforms:

| Port | Protocol | Service | Purpose |
|------|----------|---------|---------|
| 443 | TCP | HTTPS | OLM images, Ignition content, external HTTPS services |
| 6443 | TCP | Kubernetes API Server | Communication with management cluster API |
| 53 | TCP + UDP | DNS | Standard DNS queries |

!!! note
    These are the base requirements. Additional ports may be needed depending on your specific platform and workloads. Refer to the platform-specific sections below for additional port requirements.

## Konnectivity Architecture

Konnectivity uses a reverse tunnel pattern for secure communication between the control plane and worker nodes. Worker nodes initiate outbound connections to the Konnectivity server (port 8091), establishing a tunnel. The control plane then uses these pre-established tunnels to reach back into the data plane without requiring direct network access to worker nodes. This design eliminates the need for worker nodes to have publicly accessible IPs while still allowing the control plane to manage them. For detailed Konnectivity architecture and troubleshooting, see [Konnectivity in HyperShift](../../reference/konnectivity.md).

## Platform-Specific Port Configurations

Different platforms may use additional or alternative ports for specific services.

=== "AWS"

    AWS platform uses port 9440 for CAPI provider health monitoring:

    | Port | Direction | Service | Description | Code Reference |
    |------|-----------|---------|-------------|----------------|
    | 9440 | Ingress | CAPI Health Check | Health and readiness probe endpoint for AWS CAPI provider | `hypershift-operator/controllers/hostedcluster/internal/platform/aws/aws.go:222-223` |

=== "Azure"

    | Port | Direction | Service | Description | Code Reference |
    |------|-----------|---------|-------------|----------------|
    | 7443 | Ingress | Kubernetes API Server | Azure uses port 7443 (`KASSVCLBAzurePort`) instead of 6443 for the API server load balancer | `support/config/constants.go:34` - `KASSVCLBAzurePort = 7443` |

=== "GCP"

    GCP platform uses the standard common ports (6443 for API server). For private clusters, GCP uses the same private router configuration as AWS and Azure (ports 8080/8443). See the [Private Clusters](#private-clusters-awsazuregcp) section.

=== "IBM Cloud"

    | Port | Direction | Service | Description | Code Reference |
    |------|-----------|---------|-------------|----------------|
    | 2040 | Ingress | Kubernetes API Server | IBM Cloud uses port 2040 (`KASSVCIBMCloudPort`) instead of 6443 for the API server | `support/config/constants.go:37` - `KASSVCIBMCloudPort = 2040` |

    !!! note
        The ignition-server-proxy (port 8443) is not deployed on IBM Cloud platform. Workers retrieve ignition configuration through a different mechanism. See `control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver_proxy/component.go:52`.

=== "PowerVS"

    PowerVS platform uses standard ports with health monitoring on port 9440:

    | Port | Direction | Service | Description | Code Reference |
    |------|-----------|---------|-------------|----------------|
    | 9440 | Ingress | CAPI Health Check | Health and readiness probe endpoint | `hypershift-operator/controllers/hostedcluster/internal/platform/powervs/powervs.go:157` |

=== "KubeVirt"

    KubeVirt platform uses standard ports with health monitoring on port 9440:

    | Port | Direction | Service | Description | Code Reference |
    |------|-----------|---------|-------------|----------------|
    | 9440 | Ingress | CAPI Health Check | Health and readiness probe endpoint | `hypershift-operator/controllers/hostedcluster/internal/platform/kubevirt/kubevirt.go:140` |

=== "OpenStack"

    OpenStack platform uses standard ports with health monitoring on port 9440:

    | Port | Direction | Service | Description | Code Reference |
    |------|-----------|---------|-------------|----------------|
    | 9440 | Ingress | CAPI Health Check | Health and readiness probe endpoint | `hypershift-operator/controllers/hostedcluster/internal/platform/openstack/openstack.go:238` |
    | 8081 | Ingress | ORC Health Check | Health and readiness probe endpoint for OpenStack Resource Controller (4.19+) | `hypershift-operator/controllers/hostedcluster/internal/platform/openstack/openstack.go:294,311` |

=== "Agent"

    The Agent platform requires additional ports for the Cluster API (CAPI) provider and uses NodePort service publishing which requires the Ignition Proxy:

    | Port | Direction | Service | Description | Code Reference |
    |------|-----------|---------|-------------|----------------|
    | 8443 | Ingress | Ignition Proxy | HTTPS proxy for ignition content delivery (NodePort publishing) | `hypershift-operator/controllers/hostedcluster/network_policies.go:390` |
    | 8081 | Ingress | Agent CAPI Health Probe | Health check endpoint for Agent platform CAPI provider | `hypershift-operator/controllers/hostedcluster/internal/platform/agent/agent.go:96,105,115` |
    | 8080 | Ingress | Agent CAPI Metrics | Metrics endpoint for Agent platform CAPI provider (binds to localhost only) | `hypershift-operator/controllers/hostedcluster/internal/platform/agent/agent.go:97` |

=== "None"

    The None platform uses NodePort service publishing which requires the Ignition Proxy:

    | Port | Direction | Service | Description |
    |------|-----------|---------|-------------|
    | 8443 | Ingress | Ignition Proxy | HTTPS proxy for ignition content delivery (NodePort publishing) |

### Private Clusters (AWS/Azure/GCP)

For private clusters using the private router:

| Port | Direction | Service | Description | Code Reference |
|------|-----------|---------|-------------|----------------|
| 8080 | Ingress | Private Router HTTP | HTTP traffic through the private router | `hypershift-operator/controllers/hostedcluster/network_policies.go:244` |
| 8443 | Ingress | Private Router HTTPS | HTTPS traffic through the private router | `hypershift-operator/controllers/hostedcluster/network_policies.go:245` |

## Worker Node Egress Requirements

Worker nodes require outbound network access to the following HCP services:

| Port | Protocol | Service | Purpose | When Required |
|------|----------|---------|---------|---------------|
| 443 | TCP | HTTPS | Container registries, Ignition/Konnectivity (via Route), external HTTPS services | Always |
| 6443 | TCP | Kubernetes API Server | Cluster management and kubelet communication | Always |
| 7443 | TCP | Kubernetes API Server (Azure) | Cluster management on Azure platform | Azure only |
| 2040 | TCP | Kubernetes API Server (IBM Cloud) | Cluster management on IBM Cloud platform | IBM Cloud only |
| 8091 | TCP | Konnectivity Server | Establish reverse tunnel for control plane access | NodePort/LoadBalancer publishing only |
| 8443 | TCP | Ignition Proxy | Retrieve bootstrap configuration | NodePort publishing only (Agent/None platforms) |
| 53 | TCP + UDP | DNS | Name resolution | Always |

## Security Considerations

When configuring network access to the Hosted Control Plane, apply the following security principles:

### Source IP Restrictions

- **Restrict to Worker Subnets**: When using NodePort/LoadBalancer publishing, ports 8091 (Konnectivity) and 8443 (Ignition Proxy) should be restricted to worker node subnet ranges only.
- **Public Access Required**: Port 6443/7443/2040 (Kubernetes API Server) may need broader access if external clients (kubectl, CI/CD) need to reach the cluster.
- **Internal Only**: Port 8080 (Agent CAPI metrics) binds to localhost (127.0.0.1) and is not externally accessible.

### TLS Encryption

Most ports use TLS encryption with the following exceptions:

**Unencrypted (HTTP) health check endpoints:**

- Port 8081 (Agent/OpenStack CAPI Health) - HTTP health probes
- Port 9440 (CAPI Health on AWS/KubeVirt/PowerVS/OpenStack) - HTTP health probes

**Other unencrypted ports:**

- Port 53 (DNS) - standard unencrypted DNS protocol

### Sensitive Data Warning

The Ignition Server (accessed via Route on port 443 or Ignition Proxy on port 8443) serves sensitive bootstrap data including:
- Node certificates and keys
- Kubeconfig files
- Machine configuration

Ignition endpoints should be strictly limited to worker node subnets and blocked after initial bootstrap if possible.

## Network Policy Considerations

When deploying Hosted Control Planes, consider the following network policy requirements:

1. **Management Cluster to HCP**: The hypershift-operator and control-plane-operator (running on the management cluster) need unrestricted network access to pods in the HCP namespace so they can reconcile control plane components (API server, etcd, Konnectivity, etc.).

2. **HCP to Worker Nodes**: The Konnectivity tunnel enables secure communication from the control plane to worker nodes without requiring direct network access.

3. **Worker Nodes to HCP**: Worker nodes need access to the Kubernetes API Server (port 6443 or platform-specific port) and Konnectivity Server (port 8091).

4. **External Access**: If exposing services externally, ensure the appropriate service publishing strategy (LoadBalancer, NodePort, or Route) is configured. See [Exposing Services from HCP](./exposing-services-from-hcp.md) for more details.

## Firewall Configuration Example

For a typical AWS deployment using Route service publishing, ensure the following security group rules:

### Ingress Rules

```text
Port 6443/TCP     - Kubernetes API Server (from worker nodes and external clients)
Port 443/TCP      - OpenShift Router for Ignition/Konnectivity Routes (from worker nodes)
```

### Egress Rules

```text
Port 443/TCP      - HTTPS (to container registries, Routes, external services)
Port 6443/TCP     - Management cluster API (to management cluster)
Port 53/TCP+UDP   - DNS (to DNS servers)
```

!!! note "NodePort/LoadBalancer Publishing"
    If using NodePort or LoadBalancer service publishing instead of Routes, add:

    - Port 8091/TCP - Konnectivity Server (from worker nodes)
    - Port 8443/TCP - Ignition Proxy (from worker nodes during bootstrap, NodePort only)

## Related Documentation

- [Exposing Services from HCP](./exposing-services-from-hcp.md)
- [Service Publishing Strategies](./exposing-services-from-hcp.md#service-publishing-strategies)
