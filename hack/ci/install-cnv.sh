#!/usr/bin/env bash

set -ex

CNV_PRERELEASE_VERSION=${CNV_PRERELEASE_VERSION:-}

if [ -z "${CNV_PRERELEASE_VERSION}" ]
then
  CNV_RELEASE_CHANNEL=stable
  CNV_SUBSCRIPTION_SOURCE=redhat-operators
else
  CNV_RELEASE_CHANNEL=nightly-${CNV_PRERELEASE_VERSION}
  CNV_SUBSCRIPTION_SOURCE=cnv-nightly-catalog-source
fi

# The kubevirt tests require wildcard routes to be allowed
oc patch ingresscontroller -n openshift-ingress-operator default --type=json -p '[{ "op": "add", "path": "/spec/routeAdmission", "value": {wildcardPolicy: "WildcardsAllowed"}}]'

# Make the masters schedulable so we have more capacity to run VMs
oc patch scheduler cluster --type=json -p '[{ "op": "replace", "path": "/spec/mastersSchedulable", "value": true }]'

if [ -n "${CNV_PRERELEASE_VERSION}" ]
  then
  QUAY_USERNAME=${QUAY_USERNAME:-openshift-cnv+openshift_ci}
  QUAY_PULLSECRET_PATH=${QUAY_PULLSECRET_PATH:-/etc/cnv-nightly-pull-credentials/openshift_cnv_pullsecret}
  QUAY_PASSWORD=${QUAY_PASSWORD:-$(<${QUAY_PULLSECRET_PATH})}

  oc get secret pull-secret -n openshift-config -o json | jq -r '.data.".dockerconfigjson"' | base64 -d > global-pull-secret.json
  QUAY_AUTH=$(echo -n "${QUAY_USERNAME}:${QUAY_PASSWORD}" | base64 -w 0)
  jq --arg QUAY_AUTH "$QUAY_AUTH" '.auths += {"quay.io/openshift-cnv": {"auth":$QUAY_AUTH,"email":""}}' global-pull-secret.json > global-pull-secret.json.tmp
  mv global-pull-secret.json.tmp global-pull-secret.json
  oc set data secret/pull-secret -n openshift-config --from-file=.dockerconfigjson=global-pull-secret.json
  rm global-pull-secret.json

  sleep 5

  oc wait mcp master worker --for condition=updated --timeout=20m

  # Create a catalog source for the pre-release builds
  cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: cnv-nightly-catalog-source
  namespace: openshift-marketplace
spec:
  sourceType: grpc
  image: quay.io/openshift-cnv/nightly-catalog:${CNV_PRERELEASE_VERSION}
  displayName: OpenShift Virtualization Nightly Index
  publisher: Red Hat
  updateStrategy:
    registryPoll:
      interval: 8h
EOF
fi

oc apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: openshift-cnv
EOF

oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: openshift-cnv-group
  namespace: openshift-cnv
spec:
  targetNamespaces:
  - openshift-cnv
EOF

cat <<EOF | oc apply -f -
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  labels:
    operators.coreos.com/kubevirt-hyperconverged.openshift-cnv: ''
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
spec:
  channel: ${CNV_RELEASE_CHANNEL}
  installPlanApproval: Automatic
  name: kubevirt-hyperconverged
  source: ${CNV_SUBSCRIPTION_SOURCE}
  sourceNamespace: openshift-marketplace
EOF

sleep 30

RETRIES=30
CSV=
for i in $(seq ${RETRIES}); do
  if [[ -z ${CSV} ]]; then
    CSV=$(oc get subscription -n openshift-cnv kubevirt-hyperconverged -o jsonpath='{.status.installedCSV}')
  fi
  if [[ -z ${CSV} ]]; then
    echo "Try ${i}/${RETRIES}: can't get the CSV yet. Checking again in 30 seconds"
    sleep 30
  fi
  if [[ $(oc get csv -n openshift-cnv ${CSV} -o jsonpath={.status.phase}) == "Succeeded" ]]; then
    echo "CNV is deployed"
    break
  else
    echo "Try ${i}/${RETRIES}: CNV is not deployed yet. Checking again in 30 seconds"
    sleep 30
  fi
done

if [[ $(oc get csv -n openshift-cnv ${CSV} -o jsonpath={.status.phase}) != "Succeeded" ]]; then
  echo "Error: Failed to deploy CNV"
  echo "CSV ${CSV} YAML"
  oc get CSV ${CSV} -n openshift-cnv -o yaml
  echo
  echo "CSV ${CSV} Describe"
  oc describe CSV ${CSV} -n openshift-cnv
  exit 1
fi

oc create -f - <<EOF
apiVersion: hco.kubevirt.io/v1beta1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
EOF

oc wait hyperconverged -n openshift-cnv kubevirt-hyperconverged --for=condition=Available --timeout=15m
