#! /bin/bash

make control-plane-operator

./bin/control-plane-operator om proxy2 --v 2 --namespace clusters-lszaszki-hcp-cluster --hosted-control-plane lszaszki-hcp-cluster --guest-cluster-kubeconfig /Users/lszaszki/workspace/hcp/hcp-cluster.kubeconfig --management-cluster-kubeconfig /Users/lszaszki/workspace/hcp/kubeconfig-polynomial-test-hostedcluster
