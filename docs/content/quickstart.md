---
title: Quickstart
---

# HyperShift Quickstart

HyperShift is middleware for hosting OpenShift control planes at scale that
solves for cost and time to provision, as well as portability cross cloud with
strong separation of concerns between management and workloads. Clusters are
fully compliant OpenShift Container Platform (OCP) clusters and are compatible
with standard OCP and Kubernetes toolchains.

In the following instructions, shell variables are used to indicate values that 
you should adjust to your own environment.

## Prerequisites

* Go 1.17+
* Admin access to an OpenShift cluster (version 4.8+) specified by the `KUBECONFIG` environment variable
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`)
* An [AWS credentials file](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html)
  with permissions to create infrastructure for the cluster
* A valid [pull secret](https://cloud.redhat.com/openshift/install/aws/installer-provisioned) file for the `quay.io/openshift-release-dev` repository
* A Route53 public zone for the cluster's DNS records

    To create a public zone:

        DOMAIN=www.example.com
        aws route53 create-hosted-zone --name $DOMAIN --caller-reference $(whoami)-$(date --rfc-3339=date)

    NOTE: In order to access applications in your guest clusters, the public zone must be routable.

* An S3 bucket with public access to host OIDC discovery documents for your guest clusters.

    To create the bucket (in us-east-1):

        BUCKET_NAME=your-bucket-name
        aws s3api create-bucket --acl public-read --bucket $BUCKET_NAME

    To create the bucket in a region other than us-east-1:

        BUCKET_NAME=your-bucket-name
        REGION=us-east-2
        aws s3api create-bucket --acl public-read --bucket $BUCKET_NAME \
          --create-bucket-configuration LocationConstraint=$REGION \
          --region $REGION


## Create a cluster


1. Install the HyperShift CLI using Go 1.16+:

        go install github.com/openshift/hypershift@latest

1. Install HyperShift into the management cluster

    On AWS, install hypershift, specifying the OIDC bucket (see Prerequisites), its region and 
    credentials to access it:

        REGION=us-east-2
        BUCKET_NAME=your-bucket-name
        AWS_CREDS="$HOME/.aws/credentials"
        hypershift install --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
          --oidc-storage-provider-s3-credentials $AWS_CREDS \
          --oidc-storage-provider-s3-region $REGION

    If not installing on AWS, simply run:

        hypershift install

1. Create a new cluster, specifying the domain of the public
   zone provided in the prerequisites:

        CLUSTER_NAME=example
        BASE_DOMAIN=example.com
        AWS_CREDS="$HOME/.aws/credentials"
        PULL_SECRET="$HOME/pull-secret"

        hypershift create cluster aws \
        --pull-secret $PULL_SECRET \
        --aws-creds $AWS_CREDS \
        --name $CLUSTER_NAME \
        --node-pool-replicas=3 \
        --base-domain $BASE_DOMAIN \
        --generate-ssh

1. After a few minutes, check the `hostedclusters` resources in the `clusters`
   namespace and when ready it will look similar to the following:

        oc get --namespace clusters hostedclusters
        NAME      VERSION   KUBECONFIG                 AVAILABLE
        example   4.8.0     example-admin-kubeconfig   True

1. Eventually the cluster's kubeconfig will become available and can be printed to
  standard out using the `hypershift` CLI:

        hypershift create kubeconfig

## Create a NodePool

* The created cluster will have a default nodepool associated with it. However, you
   can create additional nodepools for your cluster by specifying a name, number of replicas
   and additional information such as instance type.

        NODEPOOL_NAME=${CLUSTER_NAME}-work
        INSTANCE_TYPE=m5.2xlarge
        NODEPOOL_REPLICAS=2

        hypershift create nodepool --cluster-name $CLUSTER_NAME \
          --name $NODEPOOL_NAME \
          --instance-type $INSTANCE_TYPE

    After the nodepool is created, you can query its state by listing nodepool
    resources in the `clusters` namespace:

        oc get nodepools -n clusters

## Scale a NodePool

* You can manually scale a nodepool using the `oc scale` command:

        NODEPOOL_NAME=${CLUSTER_NAME}-work
        NODEPOOL_REPLICAS=5

        oc scale nodepool/$NODEPOOL_NAME -n clusters --replicas=$NODEPOOL_REPLICAS


## Delete Cluster

* Delete the example cluster:

        hypershift destroy cluster aws \
        --aws-creds $AWS_CREDS \
        --name $CLUSTER_NAME
