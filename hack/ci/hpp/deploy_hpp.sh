#!/bin/bash

set -ex

readonly HPP_DEFAULT_STORAGE_CLASS="${HPP_DEFAULT_STORAGE_CLASS:-hostpath-csi-basic}"
readonly SCRIPT_DIR=$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")

HPP_VOLUME_SIZE=${HPP_VOLUME_SIZE:-${VOLUME_SIZE:-70}}Gi
VOLUME_BINDING_MODE="WaitForFirstConsumer"

CLUSTER_PLATFORM=$(
  oc get infrastructure cluster \
    --output=jsonpath='{$.status.platform}'
)

case "${CLUSTER_PLATFORM}" in
  Azure)
    HPP_BACKEND_STORAGE_CLASS=managed-csi
    ;;
  GCP)
    HPP_BACKEND_STORAGE_CLASS=standard-csi
    ;;
  AWS)
    HPP_BACKEND_STORAGE_CLASS=gp2
    ;;
  OpenStack)
    HPP_BACKEND_STORAGE_CLASS=local-block-hpp
    ;;
  BareMetal)
    HPP_BACKEND_STORAGE_CLASS=ocs-storagecluster-ceph-rbd
    ;;
  None)
  # UPI Installation
    HPP_BACKEND_STORAGE_CLASS=${HPP_BACKEND_STORAGE_CLASS:-ocs-storagecluster-ceph-rbd}
    ;;
  *)
    echo "[ERROR] Unsupported cluster platform: [${CLUSTER_PLATFORM}]" >&2
    exit 1
    ;;
esac

# Determine volume size for local-block-hpp SC
if [[ "${HPP_BACKEND_STORAGE_CLASS}" = "local-block-hpp" ]] ;
then
  HPP_VOLUME_SIZE=$(
    oc get persistentvolumes \
      --selector="storage.openshift.com/local-volume-owner-name=${HPP_BACKEND_STORAGE_CLASS}" \
      --output=jsonpath='{range $.items[*]}{$.spec.capacity.storage}{"\n"}{end}' \
      | sort -h | head -n 1  # Get the size of the smaller PV
  )
  if [[ -z "${HPP_VOLUME_SIZE}" ]]; then
    echo "[ERROR] Couldn't get the size of the local PVs." >&2
    echo "Please check local-storage operator configuration." >&2
    exit 1
  fi
fi

# Create HPP CustomResource using the StoragePool feature
sed "${SCRIPT_DIR}/10_hpp_pool_cr.yaml" \
  -e "s|^\( \+storage\): .*|\1: ${HPP_VOLUME_SIZE}|g" \
  -e "s|^\( \+storageClassName\): .*|\1: ${HPP_BACKEND_STORAGE_CLASS}|g" \
| oc create --filename=-

# Create HPP StorageClass using the StoragePool feature
sed "${SCRIPT_DIR}/30_hpp_pool_sc.yaml" \
  -e "s|^\(volumeBindingMode\): .*|\1: ${VOLUME_BINDING_MODE}|g" \
| oc create --filename=-


# Set HPP_DEFAULT_STORAGE_CLASS as default StorageClass for the cluster
oc get storageclass -o name | xargs oc patch -p '{"metadata": {"annotations": {"storageclass.kubernetes.io/is-default-class": "false"}}}'
oc patch storageclass "${HPP_DEFAULT_STORAGE_CLASS}" -p '{"metadata": {"annotations": {"storageclass.kubernetes.io/is-default-class": "true"}}}'
echo "[DEBUG] Printing Storage Classes:"
oc get storageclasses

# Wait for HPP to be ready
oc wait hostpathprovisioner hostpath-provisioner --for=condition='Available' --timeout='10m'

oc patch csidriver kubevirt.io.hostpath-provisioner --type merge --patch '{"spec": {"storageCapacity": false}}'
