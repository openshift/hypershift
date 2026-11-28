# Paths and binaries can be overridden via env vars (defaults match the
# container layout) so the script can be exercised by unit tests: scratch
# space lives on the pod-scoped tmp-dir emptyDir mounted at /tmp, etcd data
# on the data volume mounted at /var/lib.
SNAPSHOT_PATH="${SNAPSHOT_PATH:-/tmp/snapshot}"
DATA_DIR="${DATA_DIR:-/var/lib/data}"
RESTORE_DIR="${RESTORE_DIR:-/var/lib/restore}"
ETCDUTL_BIN="${ETCDUTL_BIN:-/usr/bin/etcdutl}"
ETCDCTL_BIN="${ETCDCTL_BIN:-/usr/bin/etcdctl}"

# If the data directory is not empty we exit early, since this means an etcd database already exists
mkdir -p "${DATA_DIR}"
[ "$(ls -A "${DATA_DIR}")" ] && echo "${DATA_DIR} not empty, not restoring snapshot" && exit 0

RESTORE_URL_VAR="RESTORE_URL_ETCD"
RESTORE_URL=${!RESTORE_URL_VAR}

# Snapshots are routinely multiple gigabytes. /tmp is the pod-scoped tmp-dir
# emptyDir, so anything left there is pinned on the node for the lifetime of
# the pod (before the read-only root filesystem, the snapshot lived in the
# init container's writable layer and was freed with it). Remove the snapshot
# on every exit path - success, the XML error below, a failed restore, and a
# download that dies mid-transfer. The trap is armed before the download so a
# partial file is cleaned up as well.
trap 'rm -f "${SNAPSHOT_PATH}"' EXIT

# When downloading from S3, curl can succeed even if the object doesn't exist
# and also when a pre-signed URL is expired.
# In this case we get an XML file which can be detected with `file` so we show
# the contents via the logs then exit with an error status
curl -o "${SNAPSHOT_PATH}" "${RESTORE_URL}"
head -c 5 "${SNAPSHOT_PATH}" | grep -q '<?xml' && cat "${SNAPSHOT_PATH}" && exit 1

# etcd 3.6+ (OCP 4.21+) moved snapshot restore/status from etcdctl to etcdutl.
# Restore to a staging directory first so a mid-write failure does not corrupt the data directory.
# HOSTNAME, HCP_NAMESPACE, and ETCD_INITIAL_CLUSTER are injected by buildEtcdInitContainer
# so each pod restores a uniquely identified member rather than the same
# 1-member default, which would cause a split-brain cluster.
PEER_URL="https://${HOSTNAME}.etcd-discovery.${HCP_NAMESPACE}.svc:2380"

rm -rf "${RESTORE_DIR}"
if [ -x "${ETCDUTL_BIN}" ]; then
  echo "INFO: using etcdutl (etcd 3.6+)"
  "${ETCDUTL_BIN}" snapshot status "${SNAPSHOT_PATH}" -w table
  "${ETCDUTL_BIN}" snapshot restore "${SNAPSHOT_PATH}" \
    --data-dir="${RESTORE_DIR}" \
    --name "${HOSTNAME}" \
    --initial-advertise-peer-urls "${PEER_URL}" \
    --initial-cluster "${ETCD_INITIAL_CLUSTER}" \
    --initial-cluster-token "${HCP_NAMESPACE}" \
    --bump-revision 1000000000 --mark-compacted
elif [ -x "${ETCDCTL_BIN}" ]; then
  echo "INFO: using etcdctl (etcd 3.5.x)"
  env ETCDCTL_API=3 "${ETCDCTL_BIN}" -w table snapshot status "${SNAPSHOT_PATH}"
  env ETCDCTL_API=3 "${ETCDCTL_BIN}" snapshot restore "${SNAPSHOT_PATH}" \
    --data-dir="${RESTORE_DIR}" \
    --name "${HOSTNAME}" \
    --initial-advertise-peer-urls "${PEER_URL}" \
    --initial-cluster "${ETCD_INITIAL_CLUSTER}" \
    --initial-cluster-token "${HCP_NAMESPACE}" \
    --bump-revision 1000000000 --mark-compacted
else
  echo "ERROR: neither etcdutl nor etcdctl found in the container image"
  exit 1
fi

rm -rf "${DATA_DIR}"
mv "${RESTORE_DIR}" "${DATA_DIR}"
