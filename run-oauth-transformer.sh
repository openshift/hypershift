#! /bin/bash

set -e

make control-plane-operator

./bin/control-plane-operator om transform-deployment --destination-deployment=destination-oauth-server-deployment.yaml --source-deployment=/Users/lszaszki/go/src/github.com/openshift/hypershift/control-plane-operator/omoperator/cmd_transform_deployment_data/standalone-oauth-openshift.yaml --target-deployment=/Users/lszaszki/go/src/github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets/oauth-openshift/deployment.yaml

cat destination-oauth-server-deployment.yaml

