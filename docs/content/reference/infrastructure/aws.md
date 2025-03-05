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
    - 2 Route 53 Hosted Zones
    - Enough quota for:
        - 1 Ingress Service Load Balancer (for Public Hosted Clusters)
        - 1 Private Link Endpoint (for Private Hosted Clusters)

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
    - Endpoint Service (Private Link)

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

!!! note

    For the Private Link networking to work, we've observed that the Endpoint zone in the hosted cluster AWS account, must match the zone of the instance resolved by the Service Endpoint in the management cluster AWS account.

    In AWS the Zone names are just alias e.g. "us-east-2b" which do not necessarily map to the same zone in different accounts.

    Because of this for Private link to be guaranteed to work, the management cluster must have subnets/workers in all zones of its region.

## IAM

In this section we want to clarify how the AWS IAM works in Hypershift context.

Hypershift expects to have the ARN roles already created in AWS, so the entity responsible to create them lies in the consumer (CLI/OCM). Hypershift tries to enable granularity to honor principle of least privilege components, which means that every component will use their own role to operate or create AWS objects and the roles are limited to what is required for the normal functioning of the product.

As an example, our CLI can handle the creation of these roles, you can follow [this article](../../how-to/aws/create-infra-iam-separately.md) to make this happen.

The HostedCluster receives the ARN roles as input, then the CLI/OCM creates an AWS permission config for each component. That lets a component to auth via [STS](https://github.com/openshift/managed-cluster-config/tree/master/resources/sts) and preconfigured oidc idp.

The roles created are consumed by some components from Hypershift, running on Control Plane and operating at Data Plane side:

These are the different roles

- **controlPlaneOperatorARN**
- **imageRegistryARN**
- **ingressARN**
- **kubeCloudControllerARN**
- **nodePoolManagementARN**
- **storageARN**
- **networkARN**

This is a sample of how this looks like the reference to the IAM Roles from the HostedCluster perspective

```yaml
...
endpointAccess: Public
  region: us-east-2
  resourceTags:
  - key: kubernetes.io/cluster/cewong-dev-bz4j5
    value: owned
rolesRef:
    controlPlaneOperatorARN: arn:aws:iam::820196288204:role/cewong-dev-bz4j5-control-plane-operator
    imageRegistryARN: arn:aws:iam::820196288204:role/cewong-dev-bz4j5-openshift-image-registry
    ingressARN: arn:aws:iam::820196288204:role/cewong-dev-bz4j5-openshift-ingress
    kubeCloudControllerARN: arn:aws:iam::820196288204:role/cewong-dev-bz4j5-cloud-controller
    networkARN: arn:aws:iam::820196288204:role/cewong-dev-bz4j5-cloud-network-config-controller
    nodePoolManagementARN: arn:aws:iam::820196288204:role/cewong-dev-bz4j5-node-pool
    storageARN: arn:aws:iam::820196288204:role/cewong-dev-bz4j5-aws-ebs-csi-driver-controller
type: AWS
...
```

And these are samples for each one of the roles Hypershift uses:


=== "**FullPermissionSet**"

    ```json
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Sid": "Statement1",
                "Effect": "Allow",
                "Action": [
                    "s3:CreateBucket",
                    "s3:DeleteBucket",
                    "s3:PutBucketTagging",
                    "s3:GetBucketTagging",
                    "s3:PutBucketPublicAccessBlock",
                    "s3:GetBucketPublicAccessBlock",
                    "s3:PutEncryptionConfiguration",
                    "s3:GetEncryptionConfiguration",
                    "s3:PutLifecycleConfiguration",
                    "s3:GetLifecycleConfiguration",
                    "s3:GetBucketLocation",
                    "s3:ListBucket",
                    "s3:GetObject",
                    "s3:PutObject",
                    "s3:DeleteObject",
                    "s3:ListBucketMultipartUploads",
                    "s3:AbortMultipartUpload",
                    "s3:ListMultipartUploadParts",
                    "elasticloadbalancing:DescribeLoadBalancers",
                    "tag:GetResources",
                    "route53:ListHostedZones",
                    "ec2:AttachVolume",
                    "ec2:CreateSnapshot",
                    "ec2:CreateTags",
                    "ec2:CreateVolume",
                    "ec2:DeleteSnapshot",
                    "ec2:DeleteTags",
                    "ec2:DeleteVolume",
                    "ec2:DescribeInstances",
                    "ec2:DescribeSnapshots",
                    "ec2:DescribeTags",
                    "ec2:DescribeVolumes",
                    "ec2:DescribeVolumesModifications",
                    "ec2:DetachVolume",
                    "ec2:ModifyVolume",
                    "ec2:DescribeInstances",
                    "ec2:DescribeInstanceStatus",
                    "ec2:DescribeInstanceTypes",
                    "ec2:UnassignPrivateIpAddresses",
                    "ec2:AssignPrivateIpAddresses",
                    "ec2:UnassignIpv6Addresses",
                    "ec2:AssignIpv6Addresses",
                    "ec2:DescribeSubnets",
                    "ec2:DescribeNetworkInterfaces",
                    "ec2:DescribeInstances",
                    "ec2:DescribeImages",
                    "ec2:DescribeRegions",
                    "ec2:DescribeRouteTables",
                    "ec2:DescribeSecurityGroups",
                    "ec2:DescribeSubnets",
                    "ec2:DescribeVolumes",
                    "ec2:CreateSecurityGroup",
                    "ec2:CreateTags",
                    "ec2:CreateVolume",
                    "ec2:ModifyInstanceAttribute",
                    "ec2:ModifyVolume",
                    "ec2:AttachVolume",
                    "ec2:AuthorizeSecurityGroupIngress",
                    "ec2:CreateRoute",
                    "ec2:DeleteRoute",
                    "ec2:DeleteSecurityGroup",
                    "ec2:DeleteVolume",
                    "ec2:DetachVolume",
                    "ec2:RevokeSecurityGroupIngress",
                    "ec2:DescribeVpcs",
                    "elasticloadbalancing:AddTags",
                    "elasticloadbalancing:AttachLoadBalancerToSubnets",
                    "elasticloadbalancing:ApplySecurityGroupsToLoadBalancer",
                    "elasticloadbalancing:CreateLoadBalancer",
                    "elasticloadbalancing:CreateLoadBalancerPolicy",
                    "elasticloadbalancing:CreateLoadBalancerListeners",
                    "elasticloadbalancing:ConfigureHealthCheck",
                    "elasticloadbalancing:DeleteLoadBalancer",
                    "elasticloadbalancing:DeleteLoadBalancerListeners",
                    "elasticloadbalancing:DescribeLoadBalancers",
                    "elasticloadbalancing:DescribeLoadBalancerAttributes",
                    "elasticloadbalancing:DetachLoadBalancerFromSubnets",
                    "elasticloadbalancing:DeregisterInstancesFromLoadBalancer",
                    "elasticloadbalancing:ModifyLoadBalancerAttributes",
                    "elasticloadbalancing:RegisterInstancesWithLoadBalancer",
                    "elasticloadbalancing:SetLoadBalancerPoliciesForBackendServer",
                    "elasticloadbalancing:AddTags",
                    "elasticloadbalancing:CreateListener",
                    "elasticloadbalancing:CreateTargetGroup",
                    "elasticloadbalancing:DeleteListener",
                    "elasticloadbalancing:DeleteTargetGroup",
                    "elasticloadbalancing:DescribeListeners",
                    "elasticloadbalancing:DescribeLoadBalancerPolicies",
                    "elasticloadbalancing:DescribeTargetGroups",
                    "elasticloadbalancing:DescribeTargetHealth",
                    "elasticloadbalancing:ModifyListener",
                    "elasticloadbalancing:ModifyTargetGroup",
                    "elasticloadbalancing:RegisterTargets",
                    "elasticloadbalancing:SetLoadBalancerPoliciesOfListener",
                    "iam:CreateServiceLinkedRole",
                    "kms:DescribeKey",
                    "ec2:AllocateAddress",
                    "ec2:AssociateRouteTable",
                    "ec2:AttachInternetGateway",
                    "ec2:AuthorizeSecurityGroupIngress",
                    "ec2:CreateInternetGateway",
                    "ec2:CreateNatGateway",
                    "ec2:CreateRoute",
                    "ec2:CreateRouteTable",
                    "ec2:CreateSecurityGroup",
                    "ec2:CreateSubnet",
                    "ec2:CreateTags",
                    "ec2:DeleteInternetGateway",
                    "ec2:DeleteNatGateway",
                    "ec2:DeleteRouteTable",
                    "ec2:DeleteSecurityGroup",
                    "ec2:DeleteSubnet",
                    "ec2:DeleteTags",
                    "ec2:DescribeAccountAttributes",
                    "ec2:DescribeAddresses",
                    "ec2:DescribeAvailabilityZones",
                    "ec2:DescribeImages",
                    "ec2:DescribeInstances",
                    "ec2:DescribeInternetGateways",
                    "ec2:DescribeNatGateways",
                    "ec2:DescribeNetworkInterfaces",
                    "ec2:DescribeNetworkInterfaceAttribute",
                    "ec2:DescribeRouteTables",
                    "ec2:DescribeSecurityGroups",
                    "ec2:DescribeSubnets",
                    "ec2:DescribeVpcs",
                    "ec2:DescribeVpcAttribute",
                    "ec2:DescribeVolumes",
                    "ec2:DetachInternetGateway",
                    "ec2:DisassociateRouteTable",
                    "ec2:DisassociateAddress",
                    "ec2:ModifyInstanceAttribute",
                    "ec2:ModifyNetworkInterfaceAttribute",
                    "ec2:ModifySubnetAttribute",
                    "ec2:ReleaseAddress",
                    "ec2:RevokeSecurityGroupIngress",
                    "ec2:RunInstances",
                    "ec2:TerminateInstances",
                    "tag:GetResources",
                    "ec2:CreateLaunchTemplate",
                    "ec2:CreateLaunchTemplateVersion",
                    "ec2:DescribeLaunchTemplates",
                    "ec2:DescribeLaunchTemplateVersions",
                    "ec2:DeleteLaunchTemplate",
                    "ec2:DeleteLaunchTemplateVersions",
                    "ec2:CreateVpcEndpoint",
                    "ec2:DescribeVpcEndpoints",
                    "ec2:ModifyVpcEndpoint",
                    "ec2:DeleteVpcEndpoints",
                    "ec2:CreateTags",
                    "route53:ListHostedZones"
                    "iam:GetRolePolicy",
                    "iam:PassRole",
                    "iam:AddRoleToInstanceProfile",
                    "iam:CreateInstanceProfile",
                    "iam:GetInstanceProfile",
                    "iam:PutRolePolicy",
                    "iam:CreateRole",
                    "iam:GetRole",
                    "iam:CreateOpenIDConnectProvider",
                    "iam:ListOpenIDConnectProviders",
                    "route53:ChangeResourceRecordSets",
                    "route53:ListResourceRecordSets",
                    "ec2:ReplaceRouteTableAssociation",
                    "ec2:AssociateDhcpOptions",
                    "ec2:CreateDhcpOptions",
                    "ec2:DescribeDhcpOptions",
                    "ec2:ModifyVpcAttribute",
                    "ec2:CreateVpc",
                    "route53:CreateHostedZone"
                ],
                "Resource": [
                    "*"
                ]
            }
        ]
    }
    ```

=== "**IngressARN**"

    ```json
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": [
                    "elasticloadbalancing:DescribeLoadBalancers",
                    "tag:GetResources",
                    "route53:ListHostedZones"
                ],
                "Resource": "*"
            },
            {
                "Effect": "Allow",
                "Action": [
                    "route53:ChangeResourceRecordSets"
                ],
                "Resource": [
                    "arn:aws:route53:::PUBLIC_ZONE_ID",
                    "arn:aws:route53:::PRIVATE_ZONE_ID"
                ]
            }
        ]
    }
    ```

=== "**ImageRegistryARN**"

    ```json
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": [
                    "s3:CreateBucket",
                    "s3:DeleteBucket",
                    "s3:PutBucketTagging",
                    "s3:GetBucketTagging",
                    "s3:PutBucketPublicAccessBlock",
                    "s3:GetBucketPublicAccessBlock",
                    "s3:PutEncryptionConfiguration",
                    "s3:GetEncryptionConfiguration",
                    "s3:PutLifecycleConfiguration",
                    "s3:GetLifecycleConfiguration",
                    "s3:GetBucketLocation",
                    "s3:ListBucket",
                    "s3:GetObject",
                    "s3:PutObject",
                    "s3:DeleteObject",
                    "s3:ListBucketMultipartUploads",
                    "s3:AbortMultipartUpload",
                    "s3:ListMultipartUploadParts"
                ],
                "Resource": "*"
            }
        ]
    }
    ```
=== "**StorageARN**"

    ```json
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": [
                    "ec2:AttachVolume",
                    "ec2:CreateSnapshot",
                    "ec2:CreateTags",
                    "ec2:CreateVolume",
                    "ec2:DeleteSnapshot",
                    "ec2:DeleteTags",
                    "ec2:DeleteVolume",
                    "ec2:DescribeInstances",
                    "ec2:DescribeSnapshots",
                    "ec2:DescribeTags",
                    "ec2:DescribeVolumes",
                    "ec2:DescribeVolumesModifications",
                    "ec2:DetachVolume",
                    "ec2:ModifyVolume"
                ],
                "Resource": "*"
            }
        ]
    }
    ```

=== "**NetworkARN**"

    ```json
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": [
                    "ec2:DescribeInstances",
                    "ec2:DescribeInstanceStatus",
                    "ec2:DescribeInstanceTypes",
                    "ec2:UnassignPrivateIpAddresses",
                    "ec2:AssignPrivateIpAddresses",
                    "ec2:UnassignIpv6Addresses",
                    "ec2:AssignIpv6Addresses",
                    "ec2:DescribeSubnets",
                    "ec2:DescribeNetworkInterfaces"
                ],
                "Resource": "*"
            }
        ]
    }
    ```

=== "**KubeCloudControllerARN**"

    ```json
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Action": [
                    "ec2:DescribeInstances",
                    "ec2:DescribeImages",
                    "ec2:DescribeRegions",
                    "ec2:DescribeRouteTables",
                    "ec2:DescribeSecurityGroups",
                    "ec2:DescribeSubnets",
                    "ec2:DescribeVolumes",
                    "ec2:CreateSecurityGroup",
                    "ec2:CreateTags",
                    "ec2:CreateVolume",
                    "ec2:ModifyInstanceAttribute",
                    "ec2:ModifyVolume",
                    "ec2:AttachVolume",
                    "ec2:AuthorizeSecurityGroupIngress",
                    "ec2:CreateRoute",
                    "ec2:DeleteRoute",
                    "ec2:DeleteSecurityGroup",
                    "ec2:DeleteVolume",
                    "ec2:DetachVolume",
                    "ec2:RevokeSecurityGroupIngress",
                    "ec2:DescribeVpcs",
                    "elasticloadbalancing:AddTags",
                    "elasticloadbalancing:AttachLoadBalancerToSubnets",
                    "elasticloadbalancing:ApplySecurityGroupsToLoadBalancer",
                    "elasticloadbalancing:CreateLoadBalancer",
                    "elasticloadbalancing:CreateLoadBalancerPolicy",
                    "elasticloadbalancing:CreateLoadBalancerListeners",
                    "elasticloadbalancing:ConfigureHealthCheck",
                    "elasticloadbalancing:DeleteLoadBalancer",
                    "elasticloadbalancing:DeleteLoadBalancerListeners",
                    "elasticloadbalancing:DescribeLoadBalancers",
                    "elasticloadbalancing:DescribeLoadBalancerAttributes",
                    "elasticloadbalancing:DetachLoadBalancerFromSubnets",
                    "elasticloadbalancing:DeregisterInstancesFromLoadBalancer",
                    "elasticloadbalancing:ModifyLoadBalancerAttributes",
                    "elasticloadbalancing:RegisterInstancesWithLoadBalancer",
                    "elasticloadbalancing:SetLoadBalancerPoliciesForBackendServer",
                    "elasticloadbalancing:AddTags",
                    "elasticloadbalancing:CreateListener",
                    "elasticloadbalancing:CreateTargetGroup",
                    "elasticloadbalancing:DeleteListener",
                    "elasticloadbalancing:DeleteTargetGroup",
                    "elasticloadbalancing:DescribeListeners",
                    "elasticloadbalancing:DescribeLoadBalancerPolicies",
                    "elasticloadbalancing:DescribeTargetGroups",
                    "elasticloadbalancing:DescribeTargetHealth",
                    "elasticloadbalancing:ModifyListener",
                    "elasticloadbalancing:ModifyTargetGroup",
                    "elasticloadbalancing:RegisterTargets",
                    "elasticloadbalancing:SetLoadBalancerPoliciesOfListener",
                    "iam:CreateServiceLinkedRole",
                    "kms:DescribeKey"
                ],
                "Resource": [
                    "*"
                ],
                "Effect": "Allow"
            }
        ]
    }
    ```

=== "**NodePoolManagementARN**"

    ```json
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Action": [
                    "ec2:AllocateAddress",
                    "ec2:AssociateRouteTable",
                    "ec2:AttachInternetGateway",
                    "ec2:AuthorizeSecurityGroupIngress",
                    "ec2:CreateInternetGateway",
                    "ec2:CreateNatGateway",
                    "ec2:CreateRoute",
                    "ec2:CreateRouteTable",
                    "ec2:CreateSecurityGroup",
                    "ec2:CreateSubnet",
                    "ec2:CreateTags",
                    "ec2:DeleteInternetGateway",
                    "ec2:DeleteNatGateway",
                    "ec2:DeleteRouteTable",
                    "ec2:DeleteSecurityGroup",
                    "ec2:DeleteSubnet",
                    "ec2:DeleteTags",
                    "ec2:DescribeAccountAttributes",
                    "ec2:DescribeAddresses",
                    "ec2:DescribeAvailabilityZones",
                    "ec2:DescribeImages",
                    "ec2:DescribeInstances",
                    "ec2:DescribeInternetGateways",
                    "ec2:DescribeNatGateways",
                    "ec2:DescribeNetworkInterfaces",
                    "ec2:DescribeNetworkInterfaceAttribute",
                    "ec2:DescribeRouteTables",
                    "ec2:DescribeSecurityGroups",
                    "ec2:DescribeSubnets",
                    "ec2:DescribeVpcs",
                    "ec2:DescribeVpcAttribute",
                    "ec2:DescribeVolumes",
                    "ec2:DetachInternetGateway",
                    "ec2:DisassociateRouteTable",
                    "ec2:DisassociateAddress",
                    "ec2:ModifyInstanceAttribute",
                    "ec2:ModifyNetworkInterfaceAttribute",
                    "ec2:ModifySubnetAttribute",
                    "ec2:ReleaseAddress",
                    "ec2:RevokeSecurityGroupIngress",
                    "ec2:RunInstances",
                    "ec2:TerminateInstances",
                    "tag:GetResources",
                    "ec2:CreateLaunchTemplate",
                    "ec2:CreateLaunchTemplateVersion",
                    "ec2:DescribeLaunchTemplates",
                    "ec2:DescribeLaunchTemplateVersions",
                    "ec2:DeleteLaunchTemplate",
                    "ec2:DeleteLaunchTemplateVersions"
                ],
                "Resource": [
                    "*"
                ],
                "Effect": "Allow"
            },
            {
                "Condition": {
                    "StringLike": {
                        "iam:AWSServiceName": "elasticloadbalancing.amazonaws.com"
                    }
                },
                "Action": [
                    "iam:CreateServiceLinkedRole"
                ],
                "Resource": [
                    "arn:*:iam::*:role/aws-service-role/elasticloadbalancing.amazonaws.com/AWSServiceRoleForElasticLoadBalancing"
                ],
                "Effect": "Allow"
            },
            {
                "Action": [
                    "iam:PassRole"
                ],
                "Resource": [
                    "arn:*:iam::*:role/*-worker-role"
                ],
                "Effect": "Allow"
            }
        ]
    }
    ```

=== "**ControlPlaneOperatorARN**"

    ```json
    {
        "Version": "2012-10-17",
        "Statement": [
            {
                "Effect": "Allow",
                "Action": [
                    "ec2:CreateVpcEndpoint",
                    "ec2:DescribeVpcEndpoints",
                    "ec2:ModifyVpcEndpoint",
                    "ec2:DeleteVpcEndpoints",
                    "ec2:CreateTags",
                    "route53:ListHostedZones"
                ],
                "Resource": "*"
            },
            {
                "Effect": "Allow",
                "Action": [
                    "route53:ChangeResourceRecordSets",
                    "route53:ListResourceRecordSets"
                ],
                "Resource": "arn:aws:route53:::%s"
            }
        ]
    }
    ```
