mkdir -p /var/lib/data
[ "$(ls -A /var/lib/data)" ] && echo "/var/lib/data not empty, skipping restore" && exit 0

[ ! -f /snapshot/snapshot.db ] && echo "ERROR: snapshot file missing" && exit 1

# Require etcdutl (etcd 3.6+ / OCP 4.21+) for --bump-revision support
if [ ! -x /usr/bin/etcdutl ]; then
  echo "ERROR: etcdutl not found — etcd 3.6+ required for --bump-revision"
  exit 1
fi

rm -rf /var/lib/restore
MEMBER_NAME=$(hostname)
PEER_URL="https://${MEMBER_NAME}.etcd-discovery.${NAMESPACE}.svc:2380"
echo "INFO: restoring with etcdutl, bump-revision=1000000000, name=${MEMBER_NAME}, peer=${PEER_URL}"
etcdutl snapshot status /snapshot/snapshot.db -w table
etcdutl snapshot restore /snapshot/snapshot.db \
  --data-dir=/var/lib/restore \
  --name="${MEMBER_NAME}" \
  --initial-cluster="${MEMBER_NAME}=${PEER_URL}" \
  --initial-advertise-peer-urls="${PEER_URL}" \
  --bump-revision 1000000000 \
  --mark-compacted

rm -rf /var/lib/data
mv /var/lib/restore /var/lib/data
echo "INFO: restore complete"
