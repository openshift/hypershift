---
title: Quickstart
---

# HyperShift Quickstart

HyperShift is middleware for hosting OpenShift control planes at scale that
solves for cost and time to provision, as well as portability cross cloud with
strong separation of concerns between management and workloads. Clusters are
fully compliant OpenShift Container Platform (OCP) clusters and are compatible
with standard OCP and Kubernetes toolchains.

## Prerequisites

* Go 1.16+
* Admin access to an OpenShift cluster (version 4.8+) specified by the `KUBECONFIG` environment variable
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`)
* An [AWS credentials file](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-files.html)
  with permissions to create infrastructure for the cluster
* A valid [pull secret](https://cloud.redhat.com/openshift/install/aws/installer-provisioned) file for the `quay.io/openshift-release-dev` repository
* A Route53 public zone for the cluster's DNS records

## Create a cluster

1. Install the HyperShift CLI using Go 1.16+:

        go install github.com/openshift/hypershift@latest

1. Install HyperShift into the management cluster:

        hypershift install

1. Create a new cluster, replacing `example.com` with the domain of the public
   zone provided in the prerequisites:

        hypershift create cluster aws \
        --pull-secret /my/pull-secret \
        --aws-creds ~/.aws/credentials \
        --name example \
        --base-domain hypershift.example.com

1. After a few minutes, check the `hostedclusters` resources in the `clusters`
   namespace and when ready it will look similar to the following:

        oc get --namespace clusters hostedclusters
        NAME      VERSION   KUBECONFIG                 AVAILABLE
        example   4.8.0     example-admin-kubeconfig   True

1. Eventually the cluster's kubeconfig will become available and can be printed to
  standard out using the `hypershift` CLI:

        hypershift create kubeconfig


1. Delete the example cluster:

        hypershift destroy cluster aws \
        --aws-creds ~/.aws/credentials \
        --namespace clusters \
        --name example
