#! /bin/bash

rm -rf input-dir
rm -rf test-output
mkdir test-output

make control-plane-operator

echo "Running Openshift Manager Operator"
./bin/control-plane-operator om --namespace clusters-lszaszki-hcp-cluster --hosted-control-plane lszaszki-hcp-cluster --input-dir input-dir --output-dir test-output --guest-cluster-kubeconfig /Users/lszaszki/workspace/hcp/hcp-cluster.kubeconfig --management-cluster-kubeconfig /Users/lszaszki/workspace/hcp/kubeconfig-polynomial-test-hostedcluster

