#! /bin/bash

set -e

make control-plane-operator

./bin/control-plane-operator om transform-deployment --destination-deployment=destination-oauth-server-deployment.yaml --source-deployment=/Users/lszaszki/go/src/github.com/openshift/hypershift/control-plane-operator/omoperator/cmd_transform_deployment_data/standalone-oauth-openshift.yaml --target-deployment=/Users/lszaszki/go/src/github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets/oauth-openshift/deployment.yaml --namespace clusters-lszaszki-hcp-cluster --hosted-control-plane lszaszki-hcp-cluster --management-cluster-kubeconfig /Users/lszaszki/workspace/hcp/kubeconfig-polynomial-test-hostedcluster

cat destination-oauth-server-deployment.yaml

