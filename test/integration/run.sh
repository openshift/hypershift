#!/bin/bash

set -o errexit
set -o nounset
set -o pipefail

if [[ -z "${PULL_SECRET:-}" ]]; then
  echo "\$PULL_SECRET is required - visit https://console.redhat.com/openshift/create/local to download a secret."
fi

echo "test"

workdir="${WORK_DIR:-}"
if [[ -z "${workdir:-}" ]]; then
  workdir="$( mktemp -d )"
  echo "Placing content in ${workdir}."
fi

kind_cluster_name="integration"
function cluster_up() {
  mkdir -p "${workdir}"
  cat <<EOF >"${workdir}/audit-policy.yaml"
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
- level: Metadata
EOF
  cat <<EOF >"${workdir}/kind-config.yaml"
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
        # enable auditing flags on the API server
        extraArgs:
          audit-log-path: /var/log/kubernetes/kube-apiserver-audit.log
          audit-policy-file: /etc/kubernetes/policies/audit-policy.yaml
        # mount new files / directories on the control plane
        extraVolumes:
          - name: audit-policies
            hostPath: /etc/kubernetes/policies
            mountPath: /etc/kubernetes/policies
            readOnly: true
            pathType: "DirectoryOrCreate"
          - name: "audit-logs"
            hostPath: "/var/log/kubernetes"
            mountPath: "/var/log/kubernetes"
            readOnly: false
            pathType: DirectoryOrCreate
  # mount the local file on the control plane
  extraMounts:
  - hostPath: ${workdir}/audit-policy.yaml
    containerPath: /etc/kubernetes/policies/audit-policy.yaml
    readOnly: true
EOF
  echo "Setting up kind cluster ${kind_cluster_name}..."
  kind create cluster --name "${kind_cluster_name}" --config "${workdir}/kind-config.yaml"
  kind get kubeconfig --name "${kind_cluster_name}" > "${workdir}/kubeconfig"
}

function cluster_down() {
  echo "Cleaning up kind cluster ${kind_cluster_name}..."
  kind delete cluster --name "${kind_cluster_name}"
  rm -rf "${workdir}"
}

function audit_log() {
  echo "Fetching audit logs to ${workdir}/kube-apiserver-audit.log"
  podman cp "${kind_cluster_name}-control-plane:/var/log/kubernetes/kube-apiserver-audit.log" "${workdir}/kube-apiserver-audit.log"
}

image_name="quay.io/hypershift/hypershift:integration-image"
function image() {
  echo "Building image..."
  make build
  os="$( source /etc/os-release && echo "${ID}" )"
  version="$( source /etc/os-release && echo "${VERSION_ID}" )"
  if [[ "${os}" != "fedora" ]]; then
    echo "Unknown OS ${os}, update this script to generate the correct FROM or use cross-compilation..."
  fi
  # we don't add the LABEL stanzas to this image, since the HyperShift Operator won't
  # be able to read the image metadata anyway, so we provide it as an annotation on the
  # HostedCluster resources the same way we provide the Control Plane Operator image
  cat <<EOF >bin/Dockerfile
FROM quay.io/${os}/${os}:${version}
COPY ./* /usr/bin
ENTRYPOINT /usr/bin/hypershift
EOF
  podman build -f bin/Dockerfile -t "${image_name}"
  kind load docker-image --name ${kind_cluster_name} "${image_name}"
}

function reload() {
  oc --kubeconfig "${workdir}/kubeconfig" get pods --all-namespaces -o json >"${workdir}/pods.json"
  jq --raw-output --arg IMAGE "${image_name}" '.items[] | select(.spec.containers[].image | index($IMAGE)) | "\(.metadata.name) --namespace \(.metadata.namespace)"' <"${workdir}/pods.json" >"${workdir}/args.txt"
  while IFS="" read -r line
  do
    oc --kubeconfig "${workdir}/kubeconfig" delete pod ${line} &
  done < "${workdir}/args.txt"
  for job in $( jobs -p ); do
    wait "${job}"
  done
}

image_labels="$( grep -Eo "LABEL .*" Dockerfile.control-plane | awk '{ print $2}' | paste -sd "," - )"
function run_setup() {
  rm -rf "${workdir}/artifacts"
  mkdir -p "${workdir}/artifacts"
  echo "Running setup..."
  go test ./test/integration -tags integration -v ${GO_TEST_FLAGS:-} \
    --timeout 0 \
    --kubeconfig "${workdir}/kubeconfig" \
    --pull-secret "${PULL_SECRET}" \
    --artifact-dir "${workdir}/artifacts" \
    --hypershift-operator-image quay.io/hypershift/hypershift:integration-image \
    --control-plane-operator-image quay.io/hypershift/hypershift:integration-image \
    --control-plane-operator-image-labels "${image_labels}" \
    --mode=setup
}

function run_test() {
  echo "Running test..."
  go test ./test/integration -tags integration -v ${GO_TEST_FLAGS:-} \
    --kubeconfig "${workdir}/kubeconfig" \
    --pull-secret "${PULL_SECRET}" \
    --artifact-dir "${workdir}/artifacts" \
    --hypershift-operator-image quay.io/hypershift/hypershift:integration-image \
    --control-plane-operator-image quay.io/hypershift/hypershift:integration-image \
    --control-plane-operator-image-labels "${image_labels}" \
    --mode=test
}

for arg in "$@"; do
  case "${arg}" in
    "cluster-up")
      cluster_up
      shift
      ;;
    "cluster-down")
      cluster_down
      shift
      ;;
    "audit-log")
      audit_log
      shift
      ;;
    "image")
      image
      shift
      ;;
    "reload")
      reload
      shift
      ;;
    "setup")
      run_setup
      shift
      ;;
    "test")
      run_test
      shift
      ;;
  esac
done
