
## AWS Components and Hypershift

As you've seen, we've been showing all the components created, managed, not managed, etc... in the Hypershift life cycle. In this section we want to dissect who creates what and what not. It contains 4 stages:

- Pre-required and Unmanaged Hosted Cluster AWS account
- Infra managed by hypershift in Management AWS account
- Infra managed by hypershift in Hosted Cluster AWS account
- Infra managed by kubernetes in Hosted Cluster AWS account

#### Pre-required and Unmanaged Infra in Hosted Cluster AWS account

=== "Public"
    - 1 VPC
    - 1 DHCP Options
    - 1 Public Subnet (for Public HostedCluster)
    - 1 Internet Gateway
    - 1 NAT Gateway
    - 1 Security Group (Worker Nodes)
    - 1 Route Tables (Public)
    - 1 Ingress Service Load Balancer

=== "Private"
    - 1 VPC
    - 1 DHCP Options
    - 1 Private Subnet (for Private HostedCluster)
    - 1 Internet Gateway
    - 1 NAT Gateway
    - 1 Security Group (Worker Nodes)
    - 1 Route Tables (Private)
    - 1 Private Link

=== "PublicAndPrivate"
    - 1 VPC
    - 1 DHCP Options
    - 2 Subnets
        - 1 Private Subnet (for Private HostedCluster)
        - 1 Public Subnet (for Public HostedCluster)
    - 1 Internet Gateway
    - 1 NAT Gateway
    - 1 Security Group (Worker Nodes)
    - 2 Route Tables (1 Private, 1 Public)
    - 2 Hosted Zones
        - 1 Ingress Service Load Balancer (for Public Hosted Clusters)
        - 1 Private Link (for Private Hosted Clusters)

#### AWS Infra Managed by Hypershift

=== "Management AWS Account"
    - ELB - Kube API Server Load Balancer

=== "Hosted Cluster AWS account"
    - Kube API Server Load Balancer
    - For NodePools:
        - EC2 Instances
        - IAM Role Policy link (with EC2 Instances)
        - IAM Profile link (with EC2 Instances)

#### AWS Infra Managed by Kubernetes

=== "Hosted Cluster"
    - Elastic Load Balancer for default ingress
    - S3 bucker for registry