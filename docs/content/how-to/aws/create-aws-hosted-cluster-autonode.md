---
title: Create AutoNode hosted clusters
---

HyperShift AutoNode (Powered by Karpenter) is a feature that runs
Karpenter management side as a control plane component while it
watches `ec2NodeClasses.karpenter.k8s.aws`, `nodePools.karpenter.sh`,
and `nodeclaims.karpenter.sh` resources in the guest cluster.

To create a hosted cluster with autoNode enabled the Hypershift Operator
needs to be installed with the `--tech-preview-no-upgrade=true` flag.

The Hypershift controller creates and manages the default [`EC2NodeClass`][ec2nodeclass]
in the hosted cluster allowing you to deploy workloads based in
your environment with [`NodePools`(`nodepools.karpenter.sh`)][nodepools].

The following steps describes how to install OpenShift workload cluster
on AWS with AutoNode feature by Karpenter.

[ec2nodeclass]: https://karpenter.sh/docs/concepts/nodeclasses/
[nodepools]: https://karpenter.sh/docs/concepts/nodepools/

## Prerequisites

- [Install the latest and greatest HyperShift CLI](https://hypershift-docs.netlify.app/getting-started/#prerequisites).
    - Make sure all prerequisites have been satisfied (Pull Secret, Hosted Zone, OIDC Bucket, etc)
- Enable AWS service-linked role for spot (one time by account where the hosted cluster will be installed):
```sh
aws iam create-service-linked-role --aws-service-name spot.amazonaws.com
```

### Create Karpenter IAM Role

Create the IAM Role used by Karpenter controller.

!!! warning "Temporary Step"
    This is a temporary step while the issue [PODAUTO-302][PODAUTO-302]
    is delivered.

[PODAUTO-302]: https://issues.redhat.com/browse/PODAUTO-302

```sh
export ROLE_NAME=KarpenterNodeRole-agl
aws iam create-role --role-name $ROLE_NAME --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Principal": {
                "Service": "ec2.amazonaws.com"
            },
            "Action": "sts:AssumeRole"
        }
    ]
}'

aws iam attach-role-policy --role-name $ROLE_NAME --policy-arn arn:aws:iam::aws:policy/AdministratorAccess
```

### Export environment variables

Export the following environment variables, adjusting according to your environment:

```sh
# AWS config
export AWS_CREDS="$HOME/.aws/credentials"
export AWS_DEFAULT_REGION=us-east-1

# OpenShift credentials and configuration
export CLUSTER_PREFIX=hcp-aws
export CLUSTER_BASE_DOMAIN=devcluster.openshift.com
export PULL_SECRET_FILE="${HOME}/.openshift/pull-secret-latest.json"
export SSH_PUB_KEY_FILE=$HOME/.ssh/id_rsa.pub

# Hypershift configuration
## S3 Bucket name hosting the OIDC discovery documents
export OIDC_BUCKET_NAME="${CLUSTER_PREFIX}-oidc"
```

### Install the Hypershift Operator

Install controller enabling `tech-preview-no-upgrade` feature gate:

```sh
./hypershift install \
  --oidc-storage-provider-s3-bucket-name="${OIDC_BUCKET_NAME}" \
  --oidc-storage-provider-s3-credentials="${AWS_CREDS}" \
  --oidc-storage-provider-s3-region="${AWS_DEFAULT_REGION}" \
  --tech-preview-no-upgrade=true
```

- Check if controller is running as expected:

```sh
oc get all -n hypershift
```

## Create Workload Cluster with AutoNode

Create the workload cluster with HyperShift AutoNode.

Choose the desired target release image name ([release controller](https://openshift-release.apps.ci.l2s4.p1.openshiftapps.com/)).

Create a hosted cluster, enabling the flag `--auto-node`:

```sh
HOSTED_CLUSTER_NAME=${CLUSTER_PREFIX}-419-ng
OCP_RELEASE_IMAGE=registry.ci.openshift.org/ocp/release:4.19.0-0.nightly-2025-01-21-163021

./hypershift create cluster aws \
  --name="${HOSTED_CLUSTER_NAME}" \
  --region="${AWS_DEFAULT_REGION}" \
  --zones="${AWS_DEFAULT_REGION}a" \
  --node-pool-replicas=1 \
  --base-domain="${CLUSTER_BASE_DOMAIN}" \
  --pull-secret="${PULL_SECRET_FILE}" \
  --aws-creds="${AWS_CREDS}" \
  --ssh-key="${SSH_PUB_KEY_FILE}" \
  --release-image="${OCP_RELEASE_IMAGE}" \
  --auto-node=true
```

Check the cluster information:

```sh
oc get --namespace clusters hostedclusters
oc get --namespace clusters nodepools
```

When completed, extract the credentials for workload cluster:

```sh
./hypershift create kubeconfig --name ${HOSTED_CLUSTER_NAME} > kubeconfig-${HOSTED_CLUSTER_NAME}

# kubeconfig for workload cluster
export KUBECONFIG=$PWD/kubeconfig-${HOSTED_CLUSTER_NAME}
```

Check the managed `EC2NodeClass` object created in the workload cluster:

```sh
oc get ec2nodeclass
```

Now you are ready to use AutoNode feature by setting the Karpenter configuration to fit your workloads.

## Deploy Sample Workloads

This section provides examples to getting started exploring HyperShift AutoNode.

### Using AutoNode with simple web app

This example demonstrate how to use AutoNode feature by creating a NodePool
to fit the sample application. The sample application is selecting the
instanceType `t3.large`, matching the `node.kubernetes.io/instance-type`
selector in the `NodePool`.

Create a Karpenter NodePool to with configuration for workload:
 
```sh
cat << EOF | oc apply -f -
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
 name: spot-and-gpu
spec:
  disruption:
    budgets:
      - nodes: 10%
    consolidateAfter: 30s
    consolidationPolicy: WhenEmptyOrUnderutilized
  weight: 10
  template:
    spec:
      expireAfter: 336h
      terminationGracePeriod: 24h0m0s
      nodeClassRef:
        group: karpenter.k8s.aws
        kind: EC2NodeClass
        name: default
      requirements:
      - key: karpenter.sh/capacity-type
        operator: In
        values: ["spot"]
      - key: node.kubernetes.io/instance-type
        operator: In
        values:
        - g4dn.xlarge
        - m5.4xlarge
        - c5.xlarge
        - t3.large
EOF
```

Deploy Sample Apps to scale Karpenter:

```sh
cat << EOF | oc apply -f -
---
apiVersion: apps/v1
kind: Deployment
metadata:
 name: web-app
spec:
 replicas: 0
 selector:
   matchLabels:
     app: web-app
 template:
   metadata:
     labels:
       app: web-app
   spec:
     affinity:
       podAntiAffinity:
         requiredDuringSchedulingIgnoredDuringExecution:
           - labelSelector:
               matchLabels:
                 app: web-app
             topologyKey: "kubernetes.io/hostname"   
     securityContext:
       runAsUser: 1000
       runAsGroup: 3000
       fsGroup: 2000
     containers:
     - image: public.ecr.aws/eks-distro/kubernetes/pause:3.2
       name: web-app
       resources:
         requests:
           cpu: "1"
           memory: 256M
       securityContext:
         allowPrivilegeEscalation: false
     nodeSelector:
       node.kubernetes.io/instance-type: "t3.large"
EOF
```

Scale the application:

```sh
oc scale --replicas=1 deployment.apps/web-app
```

Check if node joined to cluster:

```sh
oc get nodes -l karpenter.sh/nodepool=spot-and-gpu
```

Check if the application has been scheduled to the new node:

```sh
oc get pods -l app=web-app
```
