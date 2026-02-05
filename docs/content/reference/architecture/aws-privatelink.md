---
title: AWS PrivateLink
---

# AWS PrivateLink Architecture in HyperShift

## Overview

HyperShift uses AWS PrivateLink to establish secure connectivity between worker nodes in the guest cluster VPC and the hosted control plane in the management cluster VPC. This is used when `EndpointAccess` is set to `Private` or `PublicAndPrivate`.

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                              MANAGEMENT CLUSTER (ROSA Service Account)                   │
│                                                                                          │
│  ┌───────────────────────────────────────────────────────────────────────────────────┐  │
│  │                              hypershift-operator                                   │  │
│  │                                                                                    │  │
│  │   AWSEndpointServiceReconciler                                                    │  │
│  │   ├─ Watches: NodePools, HostedClusters, AWSEndpointService                       │  │
│  │   ├─ Collects SubnetIDs from NodePool.Spec.Platform.AWS.Subnet.ID                 │  │
│  │   │                                                                                │  │
│  │   ├─ ┌────────────────────────────────────────────────────────────┐               │  │
│  │   │  │  CREATES VPC ENDPOINT SERVICE (in Management VPC)          │               │  │
│  │   │  │  • CreateVpcEndpointServiceConfigurationWithContext        │               │  │
│  │   │  │  • Attaches to NLB                                         │               │  │
│  │   │  │  • Sets AcceptanceRequired: false                          │               │  │
│  │   │  │  • Manages AllowedPrincipals (CPO Role ARN)                │               │  │
│  │   │  │  • Writes EndpointServiceName to AWSEndpointService.Status │               │  │
│  │   │  └────────────────────────────────────────────────────────────┘               │  │
│  │   │                                                                                │  │
│  │   └─ Updates AWSEndpointService.Spec.SubnetIDs from NodePools                     │  │
│  └───────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                          │
│         ┌──────────────────────────────────────────────────────────────────────────┐    │
│         │                    HCP Namespace (e.g., clusters-foo)                     │    │
│         │                                                                           │    │
│         │  ┌─────────────────────────────────────────────────────────────────────┐ │    │
│         │  │                     control-plane-operator                          │ │    │
│         │  │                                                                     │ │    │
│         │  │  PrivateServiceObserver (x2)                                        │ │    │
│         │  │  ├─ Watches: kube-apiserver-private Service                         │ │    │
│         │  │  ├─ Watches: private-router Service                                 │ │    │
│         │  │  └─ Creates AWSEndpointService CR with NLB name                     │ │    │
│         │  │                                                                     │ │    │
│         │  │  AWSEndpointServiceReconciler                                       │ │    │
│         │  │  ├─ Waits for EndpointServiceName from HO                           │ │    │
│         │  │  │                                                                  │ │    │
│         │  │  ├─ ┌──────────────────────────────────────────────────────────┐   │ │    │
│         │  │  │  │  CREATES VPC ENDPOINT (Interface) (in Guest VPC)         │   │ │    │
│         │  │  │  │  • CreateVpcEndpointWithContext                          │   │ │    │
│         │  │  │  │  • Uses SubnetIDs from AWSEndpointService.Spec           │   │ │    │
│         │  │  │  │  • Creates Security Group (6443/443)                     │   │ │    │
│         │  │  │  │  • Creates Route53 DNS records                           │   │ │    │
│         │  │  │  │  • Writes EndpointID to AWSEndpointService.Status        │   │ │    │
│         │  │  │  └──────────────────────────────────────────────────────────┘   │ │    │
│         │  │  │                                                                  │ │    │
│         │  │  └─ Modifies VPC Endpoint subnets when Spec.SubnetIDs changes      │ │    │
│         │  └─────────────────────────────────────────────────────────────────────┘ │    │
│         │                                                                           │    │
│         │  ┌─────────────────┐  ┌─────────────────┐                                │    │
│         │  │ kube-apiserver- │  │  private-router │                                │    │
│         │  │ private (Svc)   │  │     (Svc)       │                                │    │
│         │  │ type:LoadBalancer│ │ type:LoadBalancer│                               │    │
│         │  └────────┬────────┘  └────────┬────────┘                                │    │
│         └───────────┼────────────────────┼─────────────────────────────────────────┘    │
│                     │                    │                                               │
└─────────────────────┼────────────────────┼───────────────────────────────────────────────┘
                      │                    │
                      ▼                    ▼
┌──────────────────────────────────────────────────────────────────────────────────────────┐
│                                         AWS                                               │
│                                                                                           │
│  ┌──────────────────────────── Management VPC ─────────────────────────────────────────┐ │
│  │                                                                                      │ │
│  │   ┌──────────────────────┐              ┌──────────────────────┐                    │ │
│  │   │    NLB (API Server)  │              │     NLB (Router)     │                    │ │
│  │   │  kube-apiserver-...  │              │   private-router-... │                    │ │
│  │   └──────────┬───────────┘              └──────────┬───────────┘                    │ │
│  │              │                                     │                                │ │
│  │              ▼                                     ▼                                │ │
│  │   ┌──────────────────────────────────────────────────────────────────────────────┐ │ │
│  │   │                      VPC ENDPOINT SERVICE(S)                                  │ │ │
│  │   │                                                                               │ │ │
│  │   │   Created by: hypershift-operator (HO)                                        │ │ │
│  │   │   • AcceptanceRequired: false                                                 │ │ │
│  │   │   • AllowedPrincipals: [CPO Role ARN, AdditionalAllowedPrincipals]           │ │ │
│  │   │   • Returns: EndpointServiceName (com.amazonaws.vpce.us-east-1.vpce-svc-*)   │ │ │
│  │   └──────────────────────────────────────────────────────────────────────────────┘ │ │
│  │                                            │                                        │ │
│  └────────────────────────────────────────────┼────────────────────────────────────────┘ │
│                                               │                                          │
│                                    AWS PrivateLink                                       │
│                                               │                                          │
│  ┌────────────────────────────────────────────┼────────────────────────────────────────┐ │
│  │                              Guest VPC     │                                         │ │
│  │                                            ▼                                         │ │
│  │   ┌──────────────────────────────────────────────────────────────────────────────┐  │ │
│  │   │                        VPC ENDPOINT (Interface)                               │  │ │
│  │   │                                                                               │  │ │
│  │   │   Created by: control-plane-operator (CPO)                                    │  │ │
│  │   │   • ServiceName: from AWSEndpointService.Status.EndpointServiceName          │  │ │
│  │   │   • SubnetIDs: from AWSEndpointService.Spec.SubnetIDs                         │  │ │
│  │   │   • SecurityGroup: allows 6443/443 from machine CIDRs                         │  │ │
│  │   └──────────────────────────────────────────────────────────────────────────────┘  │ │
│  │                                            │                                         │ │
│  │                                            ▼                                         │ │
│  │   ┌──────────────────────────────────────────────────────────────────────────────┐  │ │
│  │   │                       Route53 Private Hosted Zone                             │  │ │
│  │   │                                                                               │  │ │
│  │   │   Created by: control-plane-operator (CPO)                                    │  │ │
│  │   │   • api.<cluster>.hypershift.local → VPC Endpoint DNS                         │  │ │
│  │   │   • *.apps.<cluster>.hypershift.local → VPC Endpoint DNS                      │  │ │
│  │   └──────────────────────────────────────────────────────────────────────────────┘  │ │
│  │                                                                                      │ │
│  │   ┌────────────────┐  ┌────────────────┐  ┌────────────────┐                        │ │
│  │   │  Subnet A      │  │  Subnet B      │  │  Subnet C      │                        │ │
│  │   │  (AZ-1)        │  │  (AZ-2)        │  │  (AZ-3)        │                        │ │
│  │   │ ┌────────────┐ │  │ ┌────────────┐ │  │ ┌────────────┐ │                        │ │
│  │   │ │ ENI (VPCE) │ │  │ │ ENI (VPCE) │ │  │ │ ENI (VPCE) │ │                        │ │
│  │   │ └────────────┘ │  │ └────────────┘ │  │ └────────────┘ │                        │ │
│  │   │  Worker Nodes  │  │  Worker Nodes  │  │  Worker Nodes  │                        │ │
│  │   └────────────────┘  └────────────────┘  └────────────────┘                        │ │
│  │                                                                                      │ │
│  └──────────────────────────────────────────────────────────────────────────────────────┘ │
│                                                                                           │
└───────────────────────────────────────────────────────────────────────────────────────────┘
```

## Component Responsibilities

### AWS Resources

| AWS Resource | Created By | Description |
|--------------|------------|-------------|
| NLB (kube-apiserver-private) | control-plane-operator | Load balancer for API server traffic |
| NLB (private-router) | control-plane-operator | Load balancer for ingress router traffic |
| VPC Endpoint Service | hypershift-operator | Exposes NLBs via PrivateLink |
| VPC Endpoint (Interface) | control-plane-operator | Connects guest VPC to endpoint service |
| Security Group (for VPCE) | control-plane-operator | Allows 6443/443 from machine CIDRs |
| Route53 Private Zone records | control-plane-operator | DNS resolution for API and apps |

### Kubernetes Resources

| Resource | Created By | Responsibility |
|----------|------------|----------------|
| AWSEndpointService CR | CPO (PrivateServiceObserver) | Tracks NLB name |
| AWSEndpointService.Spec.SubnetIDs | hypershift-operator | Collected from NodePools |
| AWSEndpointService.Status.EndpointServiceName | hypershift-operator | AWS VPC Endpoint Service name |
| AWSEndpointService.Status.EndpointID | control-plane-operator | AWS VPC Endpoint ID |

## Data Flow

1. **CPO creates NLB Services** - `kube-apiserver-private` and `private-router` Services of type LoadBalancer
2. **CPO PrivateServiceObserver** watches Services, creates `AWSEndpointService` CR with NLB name
3. **HO collects SubnetIDs** from all NodePools and updates `AWSEndpointService.Spec.SubnetIDs`
4. **HO creates VPC Endpoint Service** attached to NLB, writes `EndpointServiceName` to status
5. **CPO creates VPC Endpoint** in guest VPC using the endpoint service name and subnet IDs
6. **CPO creates Route53 records** pointing to VPC Endpoint DNS

## EndpointAccess Modes

| Mode | Public NLB | Private NLB | VPC Endpoint | PrivateLink |
|------|------------|-------------|--------------|-------------|
| Public | Yes | No | No | No |
| PublicAndPrivate | Yes | Yes | Yes | Yes |
| Private | No | Yes | Yes | Yes |

## Code References

- **hypershift-operator AWS controller**: `hypershift-operator/controllers/platform/aws/controller.go`
- **control-plane-operator PrivateLink controller**: `control-plane-operator/controllers/awsprivatelink/awsprivatelink_controller.go`
- **AWSEndpointService API types**: `api/hypershift/v1beta1/endpointservice_types.go`
