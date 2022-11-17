#!/bin/bash

## HostPath Provisioner Deployment & Configuration
#  Used as a temporary workaround for https://github.com/kubernetes-sigs/gcp-compute-persistent-disk-csi-driver/issues/608

set -ex

readonly SCRIPT_DIR=$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")
readonly HCO_NAMESPACE="openshift-cnv"

echo_debug()
{
    echo "$@" >&2
}

# Wait until master and worker MCP are Updated
# or timeout after 90min.
wait_mcp_for_updated()
{
    local mcp_updated="false"

    sleep 30

    for i in {1..60}
    do
      echo_debug "Attempt ${i}/60"
      sleep 30
      if oc wait mcp --all --for condition=updated --timeout=1m; then
        echo_debug "MCP is Updated"
        mcp_updated="true"
        break
      fi
    done

    if [[ "$mcp_updated" == "false" ]]; then
      echo_debug "Error: MCP didn't get Updated!!"
      exit 1
    fi
}

# Create and mount a dedicated partition for PersistentVolumes provisioned by HPP
# =>  https://github.com/kubevirt/hostpath-provisioner-operator/blob/master/contrib/machineconfig-selinux-hpp.yaml
oc create --filename="${SCRIPT_DIR}/00_hpp_mc.yaml" -n ${HCO_NAMESPACE} || true  # Don't fail if resource already exists
wait_mcp_for_updated

# Create HPP CustomResource and StorageClass
oc create --filename="${SCRIPT_DIR}/10_hpp_cr.yaml" -n ${HCO_NAMESPACE}
oc create --filename="${SCRIPT_DIR}/20_hpp_sc.yaml"
oc create --filename="${SCRIPT_DIR}/30_hpp_csi_sc.yaml"

# Set HPP as default StorageClass for the cluster
oc annotate storageclasses --all storageclass.kubernetes.io/is-default-class-
oc annotate storageclass hostpath-provisioner storageclass.kubernetes.io/is-default-class='true'
