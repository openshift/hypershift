---
title: Create AutoNode hosted clusters
---

HyperShift AutoNode (Powered by Karpenter) is a feature that runs
Karpenter management side as a control plane component while it
watches `openshiftNodeClasses`, `nodePools.karpenter.sh`,
and `nodeclaims.karpenter.sh` resources in the guest cluster.

To create a hosted cluster with autoNode enabled the Hypershift Operator
needs to be installed with the [feature gate `--tech-preview-no-upgrade=true`][featuregate].

The Hypershift controller creates and manages the default `openshiftNodeClasses`
in the hosted cluster allowing you to deploy workloads based in
your environment with [`NodePools`(`nodepools.karpenter.sh`)][nodepools].

The following steps describes how to install OpenShift workload cluster
on AWS with AutoNode feature by Karpenter.

> Note: `openshiftNodeClasses` is exposed for [API consumers][OpenshiftEC2NodeClassSpec].

## Prerequisites

- [Install the latest and greatest HyperShift CLI](https://hypershift-docs.netlify.app/getting-started/#prerequisites).
    - Make sure all prerequisites have been satisfied (Pull Secret, Hosted Zone, OIDC Bucket, etc)
- Ensure that the AWS service-linked role for Spot is enabled in the account where the hosted cluster will be installed. This is a one-time setup per account.
    - You can verify if the role already exists using the following command:
```sh
aws iam get-role --role-name AWSServiceRoleForEC2Spot
```
    - If the role does not exist, create it with:
```sh
aws iam create-service-linked-role --aws-service-name spot.amazonaws.com
```
- Export environment variables, adjusting according to your setup:
```sh
# AWS config
export AWS_CREDS="$HOME/.aws/credentials"
export AWS_REGION=us-east-1

# OpenShift credentials and configuration
export CLUSTER_PREFIX=hcp-aws
export CLUSTER_BASE_DOMAIN=devcluster.openshift.com
export PULL_SECRET_FILE="${HOME}/.openshift/pull-secret-latest.json"
export SSH_PUB_KEY_FILE=$HOME/.ssh/id_rsa.pub

## S3 Bucket name hosting the OIDC discovery documents
# You must have set it up, see Getting Started for more information:
# https://hypershift-docs.netlify.app/getting-started/
export OIDC_BUCKET_NAME="${CLUSTER_PREFIX}-oidc"
```

### Install the Hypershift Operator

This section describes hands on steps to install the Hypershift Operator with AutoNode feature by enabling the feature gate `tech-preview-no-upgrade`. See the following documents for more information:

- [Install HyperShift Operator](https://hypershift-docs.netlify.app/getting-started/#install-hypershift-operator)
- [Feature Gates][featuregate]

Steps:

- Install the operator:
```sh
./hypershift install \
  --oidc-storage-provider-s3-bucket-name="${OIDC_BUCKET_NAME}" \
  --oidc-storage-provider-s3-credentials="${AWS_CREDS}" \
  --oidc-storage-provider-s3-region="${AWS_REGION}" \
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
HOSTED_CLUSTER_NAME=${CLUSTER_PREFIX}-wl
OCP_RELEASE_IMAGE=<CHANGE_ME_TO_LATEST_RELEASE_IMAGE>
# Example of image: quay.io/openshift-release-dev/ocp-release:4.19.0-rc.5-x86_64

./hypershift create cluster aws \
  --name="${HOSTED_CLUSTER_NAME}" \
  --region="${AWS_REGION}" \
  --zones="${AWS_REGION}a" \
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

Check the managed `openshiftNodeClasses` object created in the workload cluster:

```sh
oc get openshiftNodeClasses
```

Now you are ready to use AutoNode feature by setting the Karpenter configuration to fit your workloads.

## Deploy Sample Workloads

This section provides examples to getting started exploring HyperShift AutoNode.

### Using AutoNode with a Simple Web App

This example demonstrates how to use the AutoNode feature by creating a NodePool
to fit the sample application. The sample application selects the
instance type `t3.large`, matching the `node.kubernetes.io/instance-type`
selector in the `NodePool`.

Create a Karpenter NodePool with the configuration for the workload:
 
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
      - key: karpenter.sh/instance-family
        operator: In
        values: ["g4dn", "m5", "m6i", "c5", "c6i", "t3"]
      - key: karpenter.sh/instance-size
        operator: In
        values: ["large", "xlarge", "2xlarge"]
EOF
```

**Create a Sample App deployment:**

This section demonstrates how to deploy sample applications to test and scale Karpenter's AutoNode feature. By creating workloads with specific resource requirements, you can observe how Karpenter provisions nodes dynamically to meet the demands of your applications.

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

**Monitor and Debug Node Provisioning with AutoNode:**

Monitor the `nodeClaims` objects to track the provisioning of nodes by AutoNode. These objects provide detailed insights into the lifecycle of nodes, including their current state and any associated events. Use the following command to continuously watch the `nodeClaims`:

!!! warning "Provisioning Time"
  The provisioning process for an instance to become a node may take approximately 10 minutes. While waiting, monitor the progress using the provided commands to ensure the process completes successfully.


```sh
oc get nodeclaims --watch
```

To investigate a specific `nodeClaim` in detail, use the following command:

```sh
oc describe nodeclaim <nodeClaimName>
```

This will provide comprehensive information about the selected `nodeClaim`, helping you debug and confirm that nodes are being provisioned and functioning as expected.


**Verify Node Join Status:**

Ensure that the node has successfully joined the cluster. Use the following command to check:

```sh
oc get nodes -l karpenter.sh/nodepool=spot-and-gpu
```

This command filters the nodes associated with the `spot-and-gpu` NodePool, allowing you to confirm that the AutoNode feature is functioning as expected.

**Verify Application Scheduling:**

Ensure that the application has been successfully scheduled onto the newly provisioned node. Use the following command to check the status of the pods associated with the application:

```sh
oc get pods -l app=web-app -o wide
```

This command filters the pods by the label `app=web-app`, allowing you to confirm that the application is running on the expected node provisioned by AutoNode.


[featuregate]: https://hypershift-docs.netlify.app/how-to/feature-gates
[ec2nodeclass]: https://karpenter.sh/docs/concepts/nodeclasses/
[nodepools]: https://karpenter.sh/docs/concepts/nodepools/
[OpenshiftEC2NodeClassSpec]: https://github.com/openshift/hypershift/blob/892474ca2fd481eeab741da377d017051256914a/api/karpenter/v1beta1/karpenter_types.go#L53-L55
