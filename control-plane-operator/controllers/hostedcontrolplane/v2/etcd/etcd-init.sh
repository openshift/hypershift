# If /var/lib/data is not empty we exit early, since this means an etcd database already exists
mkdir -p /var/lib/data
[ "$(ls -A /var/lib/data)" ] && echo "/var/lib/data not empty, not restoring snapshot" && exit 0

RESTORE_URL_VAR="RESTORE_URL_ETCD"
RESTORE_URL=${!RESTORE_URL_VAR}

# When downloading from S3, curl can succeed even if the object doesn't exist
# and also when a pre-signed URL is expired.
# In this case we get an XML file which can be detected with `file` so we show
# the contents via the logs then exit with an error status
curl -o /tmp/snapshot "${RESTORE_URL}"
head -c 5 /tmp/snapshot | grep -q '<?xml' && cat /tmp/snapshot && exit 1

# etcd 3.6+ (OCP 4.21+) moved snapshot restore/status from etcdctl to etcdutl.
# Restore to a staging directory first so a mid-write failure does not corrupt /var/lib/data.
# HOSTNAME, HCP_NAMESPACE, and ETCD_INITIAL_CLUSTER are injected by buildEtcdInitContainer
# so each pod restores a uniquely identified member rather than the same
# 1-member default, which would cause a split-brain cluster.
PEER_URL="https://${HOSTNAME}.etcd-discovery.${HCP_NAMESPACE}.svc:2380"

rm -rf /var/lib/restore
if [ -x /usr/bin/etcdutl ]; then
  echo "INFO: using etcdutl (etcd 3.6+)"
  etcdutl snapshot status /tmp/snapshot -w table
  etcdutl snapshot restore /tmp/snapshot \
    --data-dir=/var/lib/restore \
    --name "${HOSTNAME}" \
    --initial-advertise-peer-urls "${PEER_URL}" \
    --initial-cluster "${ETCD_INITIAL_CLUSTER}" \
    --initial-cluster-token "${HCP_NAMESPACE}" \
    --bump-revision 1000000000 --mark-compacted
elif [ -x /usr/bin/etcdctl ]; then
  echo "INFO: using etcdctl (etcd 3.5.x)"
  env ETCDCTL_API=3 /usr/bin/etcdctl -w table snapshot status /tmp/snapshot
  env ETCDCTL_API=3 /usr/bin/etcdctl snapshot restore /tmp/snapshot \
    --data-dir=/var/lib/restore \
    --name "${HOSTNAME}" \
    --initial-advertise-peer-urls "${PEER_URL}" \
    --initial-cluster "${ETCD_INITIAL_CLUSTER}" \
    --initial-cluster-token "${HCP_NAMESPACE}" \
    --bump-revision 1000000000 --mark-compacted
else
  echo "ERROR: neither etcdutl nor etcdctl found in the container image"
  exit 1
fi

rm -rf /var/lib/data
mv /var/lib/restore /var/lib/data
