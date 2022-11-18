
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
    - Route 53 Hosted zones
        - Domain to host Private and Public entries for HostedClusters

#### Infra pre-required and unmanaged in Hosted Cluster AWS account

=== "All access Modes"
    - 1 VPC
    - 1 DHCP Options
    - 2 Subnets
        - Private subnet - internal data plane subnet
        - Public subnet - enable access to the internet from the data plane
    - 1 Internet Gateway
    - 1 Elastic IP
    - 1 NAT Gateway
    - 1 Security Group (Worker Nodes)
    - 2 Route Tables (1 Private, 1 Public)
    - 2 Hosted Zones
        - Enough quota for:
            - 1 Ingress Service Load Balancer (for Public Hosted Clusters)
            - 1 Private Link (for Private Hosted Clusters)

#### AWS Infra Managed by Hypershift

- **Public**

=== "Management Cluster AWS Account"
    - NLB - Load Balancer Kube API Server
        - Kubernetes creates a Security Group
    - Volumes
        - For ETCD (1 or 3 depending on HA)
        - For ovn-Kube

=== "Hosted Cluster AWS account"
    - For NodePools:
        - EC2 Instances
            - Need the Role and RolePolicy

- **Private**

=== "Management Cluster AWS Account"
    - NLB - Load Balancer Private Router
    - Enpoint Service (Private Link)

=== "Hosted Cluster AWS account"
    - Private Link Endpoints
        - 1 Endpoint per Availability Zone
    - For NodePools:
        - EC2 Instances

- **PublicAndPrivate**

=== "Management Cluster AWS Account"
    - 1 NLB - Load Balancer Public Router
    - 1 NLB - Load Balancer Private Router
    - Enpoint Service (Private Link)
    - Volumes
        - For ETCD (1 or 3 depending on HA)
        - For ovn-Kube

=== "Hosted Cluster AWS account"
    - Private Link Endpoints
        - 1 Endpoint per Availability Zone
    - For NodePools:
        - EC2 Instances

#### AWS Infra Managed by Kubernetes

=== "Hosted Cluster AWS account"
    - Network Load Balancer for default ingress
    - S3 bucket for registry