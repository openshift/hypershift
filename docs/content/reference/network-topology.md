---
title: Network Topology
---

# Network Topology

Interactive diagrams showing HyperShift network connectivity across platforms and topology variants.
Select a topology from the dropdown to explore how control plane and data plane components communicate,
which flows traverse the Konnectivity tunnel, and how services are published.

<iframe src="network-topology-viewer.html" width="100%" height="800" frameborder="0" style="border: 1px solid #ccc; border-radius: 4px;"></iframe>

## Available Topologies

| Topology | Platform | Description |
|----------|----------|-------------|
| AWS Public | AWS | Public endpoints via Route (KAS, OAuth, Konnectivity) and S3 (Ignition) |
| AWS Public + ExternalDNS | AWS | Same as public but with ExternalDNS managing DNS records for service endpoints |
| AWS Private | AWS | Private endpoints via PrivateLink; KAS accessible only within VPC |
| Azure ARO HCP | Azure | Managed Azure with shared ingress via HAProxy, Swift networking for pods |
| Azure Self-Managed | Azure | Self-managed Azure with Route-based service publishing |
| Agent (NodePort) | Bare Metal | Agent/bare-metal platform using NodePort for all service endpoints |

## Edge Legend

- **Blue (solid)** — Direct network flow (e.g., client to KAS, KAS to etcd)
- **Amber (solid)** — Flow tunneled through Konnectivity (e.g., KAS to kubelet, metrics scraping)
- **Green (dashed)** — Konnectivity tunnel establishment (agent connects to server)
- **Purple (solid)** — PrivateLink / private network flow
- **Gray (solid)** — Internal cluster communication

## Related

- [Konnectivity](konnectivity.md) — architecture and troubleshooting for the Konnectivity tunnel
- [Service Publishing Strategies](service-publishing-strategies.md) — how HCP services are exposed per platform
