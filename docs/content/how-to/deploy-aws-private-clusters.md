---
title: Deploy AWS private clusters
---

# Deploying AWS private clusters

## Create a hypershift-operator IAM user in the management account

**NOTE: An IAM Role can also be used but this is the simpliest method to document**

Create the policy document
```
# cat << EOF >> policy.json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
                "ec2:CreateVpcEndpointServiceConfiguration",
                "ec2:DescribeVpcEndpointServiceConfigurations",
                "ec2:DeleteVpcEndpointServiceConfigurations",
                "elasticloadbalancing:DescribeLoadBalancers"
            ],
            "Resource": "*"
        }
    ]
}
EOF
```

Create the policy
```
# aws iam create-policy --policy-name=hypershift-operator-policy --policy-document=file://policy.json
{
    "Policy": {
        "PolicyName": "hypershift-operator-policy",
        "PolicyId": "...",
        "Arn": "arn:aws:iam::...:policy/hypershift-operator-policy",
        "Path": "/",
        "DefaultVersionId": "v1",
        "AttachmentCount": 0,
        "PermissionsBoundaryUsageCount": 0,
        "IsAttachable": true,
        "CreateDate": "2021-11-30T16:24:56+00:00",
        "UpdateDate": "2021-11-30T16:24:56+00:00"
    }
}
```

Create the hypershift-operator user
```
# aws iam create-user --user-name=hypershift-operator
{
    "User": {
        "Path": "/",
        "UserName": "hypershift-operator",
        "UserId": "...",
        "Arn": "arn:aws:iam::...:user/hypershift-operator",
        "CreateDate": "2021-11-30T16:26:37+00:00"
    }
}
```

Attach user policy (use policy-arn from `create-policy` output)
```
aws iam attach-user-policy --user-name=hypershift-operator --policy-arn=arn:aws:iam::...:policy/hypershift-operator-policy
```

Create access key
```
# aws iam create-access-key --user-name=hypershift-operator
{
    "AccessKey": {
        "UserName": "hypershift-operator",
        "AccessKeyId": "...",
        "Status": "Active",
        "SecretAccessKey": "...",
        "CreateDate": "2021-11-30T16:31:19+00:00"
    }
}
```

Create the credentials file (copy `AccessKeyId` and `SecretAccessKey` from `create-access-key` output)
```
# cat << EOF >> aws-private-creds
[default]
aws_access_key_id = <secret>
aws_secret_access_key = <secret>
EOF
```

## Install hypershift-operator

```
# ./bin/hypershift install \
  --private-platform=AWS \
  --aws-private-creds=aws-private-creds \
  --aws-private-region=us-west-1 \
  --oidc-storage-provider-s3-bucket-name=$BUCKET_NAME \
  --oidc-storage-provider-s3-credentials=$HOME/hypershift/aws-creds \
  --oidc-storage-provider-s3-region=us-west-1
```

## Create the HostedCluster

Create the manifests
```
# ./bin/hypershift create cluster aws \
  --aws-creds=$AWS_CREDS \
  --pull-secret=$PULL_SECRET \
  --ssh-key=$SSH_KEY \
  --region=us-west-1 \
  --base-domain=hypershift.example.com \
  --render > cluster.yaml
```

Edit the HostedCluster with `endpointAccess: Private`
```
# vi cluster.yaml
...
apiVersion: hypershift.openshift.io/v1alpha1
kind: HostedCluster
metadata:
  name: example
  namespace: clusters
spec:
...
  platform:
    aws:
      endpointAccess: Private <--
```

Create the HostedCluster resources
```
# oc create -f cluster.yaml
```

## Observe HostedCluster rollout

### Watch AWSEndpointServices
AWSEndpointService CRs are created when the following services are created
* `kube-apiserver-private` in the HCP namespace
* `router-$hcpNamespace` in the `openshift-ingress` namespace

```
# oc get awsendpointservice
NAME                      AGE
kube-apiserver-private    64m
router-clusters-example   64m
```

The AWSEndpointServices are acted upon by two different controllers, one in the `hypershift-operator` (HO) which creates the Endpoint Services in the management account, and one in the `control-plane-operator` (CPO) which creates the Endpoints and `$hcpName.hypershift.local` DNS zone that contains CNAME records for `api.$hcpName.hypershift.local` and `*.apps.$hcpName.hypershift.local` whose value is the DNS name of the corresponding Endpoint.

The AWSEndpointServices will go through several states:
* LB not active
    * AWS is still activiating the NLB for the service type LoadBalancer)
    * takes 2-3m
* EndpointService is created by HO
    * `status.endpointServiceName` is set and `EndpointServiceAvailable` condition is `True`
    * immediate
* Endpoint and DNS records are created CPO
    * `status.{endpointID,dnsZoneID,dnsName}` are set and `EndpointAvailable` condition is `True`
    * takes ~30s

## Access the private guest cluster

Start a bastion
```
# hypershift create bastion aws --aws-creds=$AWS_CREDS --infra-id=$INFRA_ID --region=us-west-1 --ssh-key-file=$SSH_KEY
2021-11-30T09:54:36-06:00       INFO    Created security group  {"name": "...", "id": "..."}
2021-11-30T09:54:37-06:00       INFO    Created key pair        {"id": "...", "name": "..."}
2021-11-30T09:54:39-06:00       INFO    Created ec2 instance    {"id": "...", "name": "..."}
2021-11-30T09:55:24-06:00       INFO    Successfully created bastion    {"id": "...", "publicIP": "54.177.195.110"}
```

Get private IP of nodes in the NodePool
```
# aws ec2 describe-instances --filter="Name=tag:kubernetes.io/cluster/$CLUSTER_NAME,Values=owned" | jq '.Reservations[] | .Instances[] | select(.PublicDnsName=="") | .PrivateIpAddress'
"10.0.133.98"
"10.0.136.236"
```

Dump kubeconfig for the HostedCluster to copy to the node
```
# ./bin/hypershift create kubeconfig
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: <redacted>
    server: https://api.example.hypershift.local:6443
  name: clusters-example
...
```

SSH into one of the nodes via the bastion (bastion IP from the `create bastion` output)
```
# ssh -o ProxyCommand="ssh ec2-user@54.177.195.110 -W %h:%p" core@10.0.133.98
```

Copy the kubeconfig contents to a file on the node
```
# cat << EOF >> kubeconfig
<paste kubeconfig contents>
EOF
# export KUBECONFIG=$PWD/kubeconfig
```

Observe guest cluster status
```
# oc get clusteroperators
...
# oc get clusterversion
...
```