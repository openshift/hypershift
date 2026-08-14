#!/usr/bin/env bash

set -ex

# This script does the following
# 1. Installs MCE from a custom catalog source
# 2. makes the OCP cluster a "local-cluster"
# 3. enables the hypershift operator.
#
# Once this script executes successfully, the standard hypershift cli tools
# and hypershift e2e test suite can be executed.
#
#
# Prerequisites
# - oc tool installed
# - jq cli tool is installed
# - KUBECONFIG env var is set to point to an OCP cluster you want to install on.
# - QUAY_AUTH env var is set to a quay token that is authorized to pull from quay.io/acm-d
#


MCE_CHANNEL=${MCE_CHANNEL:-"stable-2.1"}
MCE_DEV_RELEASE_IMAGE=${MCE_DEV_RELEASE_IMAGE:-"quay.io/acm-d/mce-custom-registry:2.1-latest"}

if [ -z "$QUAY_AUTH" ]; then
	echo "QUAY_AUTH env var required"
	exit 1
fi

# Setup quay mirror container repo
cat << EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: ImageContentSourcePolicy
metadata:
  name: rhacm-repo
spec:
  repositoryDigestMirrors:
  - mirrors:
    - quay.io:443/acm-d
    source: registry.redhat.io/rhacm2
  - mirrors:
    - quay.io:443/acm-d
    source: registry.redhat.io/multicluster-engine
  - mirrors:
    - registry.redhat.io/openshift4/ose-oauth-proxy
    source: registry.access.redhat.com/openshift4/ose-oauth-proxy"
EOF

oc get secret pull-secret -n openshift-config -o json | jq -r '.data.".dockerconfigjson"' | base64 -d > global-pull-secret.json
jq --arg QUAY_AUTH "$QUAY_AUTH" '.auths += {"quay.io:443": {"auth":$QUAY_AUTH,"email":""}}' global-pull-secret.json > global-pull-secret.json.tmp
mv global-pull-secret.json.tmp global-pull-secret.json
oc set data secret/pull-secret -n openshift-config --from-file=.dockerconfigjson=global-pull-secret.json
sleep 5
oc wait mcp master worker --for condition=updated --timeout=20m

# Install MCE custom catalog source
oc apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: multicluster-engine
EOF

oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: multiclusterengine-catalog
  namespace: openshift-marketplace
spec:
  displayName: MultiCluster Engine
  publisher: Red Hat
  sourceType: grpc
  image: ${MCE_DEV_RELEASE_IMAGE}
  updateStrategy:
    registryPoll:
      interval: 10m
EOF

oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: multicluster-engine-group
  namespace: multicluster-engine
spec:
  targetNamespaces:
    - "multicluster-engine"
EOF

oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: multicluster-engine
  namespace: multicluster-engine
spec:
  channel: ${MCE_CHANNEL}
  installPlanApproval: Automatic
  name: multicluster-engine
  source: multiclusterengine-catalog
  sourceNamespace: openshift-marketplace
EOF

# Wait for MCE to install
sleep 10
CSVName=""
for run in {1..10}; do
  output=$(oc get sub multicluster-engine -n multicluster-engine -o jsonpath='{.status.currentCSV}' >> /dev/null && echo "exists" || echo "not found")
  if [ "$output" != "exists" ]; then
    sleep 2
    continue
  fi
  CSVName=$(oc get sub -n multicluster-engine multicluster-engine -o jsonpath='{.status.currentCSV}')
  if [ "$CSVName" != "" ]; then
    break
  fi
  sleep 10
done

_apiReady=0
echo "* Using CSV: ${CSVName}"
for run in {1..10}; do
  sleep 10
  output=$(oc get csv -n multicluster-engine $CSVName -o jsonpath='{.status.phase}' >> /dev/null && echo "exists" || echo "not found")
  if [ "$output" != "exists" ]; then
    continue
  fi
  phase=$(oc get csv -n multicluster-engine $CSVName -o jsonpath='{.status.phase}')
  if [ "$phase" == "Succeeded" ]; then
    _apiReady=1
    break
  fi
  echo "Waiting for CSV to be ready"
done

if [ $_apiReady -eq 0 ]; then
  echo "multiclusterengine subscription could not install in the allotted time."
  exit 1
fi

echo "multiclusterengine installed successfully"

# Enable Hypershift Preview


oc apply -f - <<EOF
apiVersion: multicluster.openshift.io/v1
kind: MultiClusterEngine
metadata:
  name: multiclusterengine-sample
spec: {}
EOF

sleep 5

oc patch mce multiclusterengine-sample --type=merge -p '{"spec":{"overrides":{"components":[{"name":"hypershift-preview","enabled": true}]}}}'

# It takes some time for this api to become available.
# So we try multiple times until it succeeds
_localClusterCreated=0
set +e
for run in {1..10}; do
  oc apply -f - <<EOF
apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  labels:
    local-cluster: "true"
  name: local-cluster
spec:
  hubAcceptsClient: true
  leaseDurationSeconds: 60
EOF
  if [ $? -eq 0 ]; then
    _localClusterCreated=1
    break
  fi
  sleep 10
done
set -e

if [ $_localClusterCreated -eq 0 ]; then
  echo "local cluster not created in the allotted time."
  exit 1
fi


oc apply -f - <<EOF
apiVersion: addon.open-cluster-management.io/v1alpha1
kind: ManagedClusterAddOn
metadata:
  name: hypershift-addon
  namespace: local-cluster
spec:
  installNamespace: open-cluster-management-agent-addon
EOF

# wait for hypershift operator to come online
_hypershiftReady=0
set +e
for run in {1..10}; do
  oc get pods -n hypershift | grep "operator.*Running"
  if [ $? -eq 0 ]; then
    _hypershiftReady=1
    break
  fi
  echo "Waiting on hypershift operator to install"
  sleep 15
done
set -e

if [ $_hypershiftReady -eq 0 ]; then
  echo "hypershift operator did not come online in expected time"
  exit 1
fi

echo "hypershift is online!"

