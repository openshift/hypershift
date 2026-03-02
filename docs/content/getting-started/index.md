---
title: Getting Started with HyperShift
description: Choose your platform and get started with HyperShift hosted clusters
---

# Getting Started with HyperShift

HyperShift is middleware for hosting OpenShift control planes at scale that solves for cost and time to provision, as well as portability across cloud providers with strong separation of concerns between management and workloads.

## Choose Your Platform

Select the platform where you want to deploy your HyperShift hosted clusters:

=== "AWS"

    **Amazon Web Services** - The most mature and feature-complete platform for HyperShift

    - ✅ **Production Ready**: Fully tested and supported
    - ✅ **ROSA HCP**: Managed service option available
    - ✅ **Self-Managed**: Full control over deployment
    - ✅ **AutoNode**: Karpenter-based auto-scaling

    [**Get Started with AWS →**](aws.md)

=== "Azure"

    **Microsoft Azure** - ARO with Hosted Control Planes

    - ✅ **ARO HCP**: Fully managed by Microsoft and Red Hat
    - ✅ **Enterprise Integration**: Azure AD, monitoring, compliance
    - ⚠️ **Managed Only**: No self-managed option

    [**Get Started with Azure →**](azure.md)

=== "IBM Cloud"

    **IBM Cloud PowerVS** - Power architecture workloads

    - ✅ **Power Architecture**: POWER9/10 processors
    - ✅ **High Performance**: Memory-intensive workloads
    - ⚠️ **Specialized**: Best for Power-specific applications

    [**Get Started with IBM Cloud →**](ibm-cloud.md)

=== "KubeVirt"

    **Virtualized Infrastructure** - VM-based hosted clusters

    - ✅ **Nested Virtualization**: Clusters as VMs
    - ✅ **Development Friendly**: Great for testing
    - ⚠️ **Performance Overhead**: Virtual machine layer

    [**Get Started with KubeVirt →**](kubevirt.md)

=== "Agent"

    **Bare Metal & Edge** - On-premises and edge deployments

    - ✅ **Bare Metal**: Direct hardware deployment
    - ✅ **Edge Computing**: Distributed locations
    - ✅ **Air-Gapped**: Disconnected environments
    - ⚠️ **Complex Setup**: Requires hardware management

    [**Get Started with Agent →**](agent.md)

=== "OpenStack"

    **Private Cloud** - OpenStack-based deployments

    - ✅ **Private Cloud**: On-premises cloud infrastructure
    - ✅ **Development**: Testing and development environments
    - ⚠️ **Limited Production**: Primarily for development use

    [**Get Started with OpenStack →**](openstack.md)

## Quick Comparison

| Platform | Maturity | Use Case | Management Model |
|----------|----------|----------|------------------|
| **AWS** | Production | General purpose, ROSA service | Managed + Self-managed |
| **Azure** | Production | Azure-native workloads | Managed only (ARO HCP) |
| **IBM Cloud** | Stable | Power architecture, databases | Self-managed |
| **KubeVirt** | Stable | Development, testing, nested | Self-managed |
| **Agent** | Stable | Bare metal, edge, air-gapped | Self-managed |
| **OpenStack** | Development | Private cloud, testing | Self-managed |

## What You'll Need

Before starting with any platform, you'll typically need:

- [x] **Management Cluster**: OpenShift 4.14+ or Kubernetes 1.27+
- [x] **HyperShift CLI**: Download from releases or build from source
- [x] **Platform Credentials**: Cloud provider or infrastructure access
- [x] **Pull Secret**: Red Hat registry access for OpenShift images

## Next Steps

1. **Choose a platform** from the tabs above
2. **Follow the platform-specific guide** for detailed setup instructions
3. **Deploy your first hosted cluster** and start exploring HyperShift capabilities

## Need Help?

- 📚 **Documentation**: Each platform guide includes troubleshooting
- 💬 **Community**: [HyperShift Slack Channel](https://kubernetes.slack.com/channels/hypershift)
- 🐛 **Bug reports**: [Red Hat Customer Portal](https://issues.redhat.com/)
- 🎯 **Support**: Red Hat support for managed services

---

*Ready to get started? Pick your platform above and deploy your first HyperShift hosted cluster!*