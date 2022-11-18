
## Infrastructure - AWS

In this section we want to dissect who creates what and what not. It contains 4 stages:

- Infra pre-required and unmanaged for hypershift operator in an arbitrary AWS account
- Infra pre-required and unmanaged in Hosted Cluster AWS account
- Infra managed by hypershift in Management AWS account
- Infra managed by hypershift in Hosted Cluster AWS account
- Infra managed by kubernetes in Hosted Cluster AWS account

!!! note
    The arbitrary AWS account depends on who is providing the Hypershift service.

    - **Self Managed:** It will be controlled by the [Cluster Service Provider](../concepts-and-personas.md#personas).
    - **SaaS:** In this case the AWS Account will belong to Red Hat.

#### Infra pre-required and unmanaged for hypershift operator in an arbitrary AWS account

=== "Management Cluster AWS Account"
    - 1 S3 Bucket
        - OIDC
        - OLM Registry for Private Hosted Cluster
    - Route 53 Hosted zones
        - Domain to host Private and Public entries for HostedClusters

#### Infra pre-required and unmanaged in Hosted Cluster AWS account

=== "Public"
    - 1 VPC
    - 1 DHCP Options
    - 2 Subnets
        - 1 Public Subnet (for Public HostedCluster)
        - 1 Private Subnet (Access from the Instances, Dataplane)
    - 1 Internet Gateway
    - 1 Elastic IP
    - 1 NAT Gateway
    - 1 Security Group (Worker Nodes)
    - 1 Route Tables (Public)
    - 1 Ingress Service Load Balancer

=== "Private"
    - 1 VPC
    - 1 DHCP Options
    - 1 Private Subnet (Access from the Instances, Dataplane)
    - 1 Internet Gateway
    - 1 Elastic IP
    - 1 NAT Gateway
    - 1 Security Group (Worker Nodes)
    - 1 Route Tables (Private)
    - 1 Private Link

=== "PublicAndPrivate"
    - 1 VPC
    - 1 DHCP Options
    - 2 Subnets
        - 1 Public Subnet (for Public HostedCluster)
        - 1 Private Subnet (Access from the Instances, Dataplane)
    - 1 Internet Gateway
    - 1 Elastic IP
    - 1 NAT Gateway
    - 1 Security Group (Worker Nodes)
    - 2 Route Tables (1 Private, 1 Public)
    - 2 Hosted Zones
        - 1 Ingress Service Load Balancer (for Public Hosted Clusters)
        - 1 Private Link (for Private Hosted Clusters)

#### AWS Infra Managed by Hypershift

=== "Management Cluster AWS Account"
    - ELB - Kube API Server Load Balancer
    - Private Link Services

=== "Hosted Cluster AWS account"
    - Kube API Server Load Balancer
    - Private Link Endpoints
    - For NodePools:
        - EC2 Instances
        - IAM Role Policy link (with EC2 Instances)
        - IAM Profile link (with EC2 Instances)

#### AWS Infra Managed by Kubernetes

=== "Hosted Cluster AWS account"
    - Elastic Load Balancer for default ingress
    - S3 bucker for registry