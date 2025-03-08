#!/usr/bin/env bash
set -eo pipefail

# Prerequisites:
# 1. This should only run in a guest cluster with autoNode, in a testing environment, and with a valid kubeconfig
# 2. The HCP should be annotated with hypershift.openshift.io/karpenter-core-e2e-override=true
# 3. There should exist a default EC2NodeClass in the guest cluster
# 4. The openshift/kubernetes-sigs-karpenter fork should be cloned somewhere and pointed to by the KARPENTER_CORE_DIR

if [[ -z "$KARPENTER_CORE_DIR" ]]; then
  echo "KARPENTER_CORE_DIR is not set"
  exit 1
fi

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

# snapshot the default nodeclass so that the upstream tests can recreate them before each test
TMPFILE=$(mktemp)
oc get ec2nodeclasses.karpenter.k8s.aws default -o yaml > "$TMPFILE"
"$SCRIPT_DIR/adjust-ec2nodeclass.sh" "$TMPFILE"

DEFAULT_NODEPOOL=${KARPENTER_DEFAULT_NODECLASS:-"$SCRIPT_DIR/default_nodepool.yaml"}

CLEANUP=${CLEANUP:-true}

cleanup() {
  echo "Cleaning up..."
  for node in $(oc get nodes -o name); do
    oc adm taint nodes "$node" key:NoSchedule-
  done

  for cronjob in $(oc get cronjobs -o name); do
    oc patch "$cronjob" -p '{"spec" : {"suspend" : false }}'
  done

  oc delete nodepool --all
  # NOTE: cleanup requires the management cluster to remove the hcp annotation and potentially restart the karpenter-operator
}

if [[ "$CLEANUP" == "true" ]]; then
  trap cleanup EXIT
fi

# tests expect all nodes to be tainted before running:
# https://github.com/kubernetes-sigs/karpenter/blob/main/test/pkg/environment/common/setup.go#L87
echo "Tainting all nodes..."
for node in $(oc get nodes -o name); do
  oc adm taint nodes "$node" key=value:NoSchedule --overwrite
done

# pause cronjobs, otherwise suite fails if it detects schedulable pods before each test
echo "Pausing all running cronjobs..."
for cronjob in $(oc get cronjobs -o name); do
  oc patch "$cronjob" -p '{"spec" : {"suspend" : true }}'
done

# then remove all NodeClasses, VAP, and stray NodePools
oc delete validatingadmissionpolicies.admissionregistration.k8s.io karpenter.ec2nodeclass.hypershift.io
oc delete ec2nodeclasses.karpenter.k8s.aws --all --timeout 60s
oc delete nodepool --all

GO111MODULE=on GOWORK=off go test \
  -C "$KARPENTER_CORE_DIR" \
  -count 1 \
  -timeout 1h \
  -v \
  "$KARPENTER_CORE_DIR"/test/suites/$(echo "$TEST_SUITE" | tr A-Z a-z)/... \
  --ginkgo.focus="${FOCUS}" \
  --ginkgo.timeout=1h \
  --ginkgo.grace-period=5m \
  --ginkgo.vv \
  --default-nodeclass="$TMPFILE" \
  --default-nodepool="$DEFAULT_NODEPOOL"
