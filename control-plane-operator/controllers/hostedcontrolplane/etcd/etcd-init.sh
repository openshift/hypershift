# If /var/lib/data is not empty we exit early, since this means an etcd database already exists
mkdir -p /var/lib/data
[ "$(ls -A /var/lib/data)" ] && echo "/var/lib/data not empty, not restoring snapshot" && exit 0

# HOSTNAME is e.g etcd-0
# so HOSTNAME_SUFFIX is etcd_0, then we uppercase it with ^^ so RESTORE_URL_VAR becomes RESTORE_URL_ETCD_0
HOSTNAME_SUFFIX=${HOSTNAME/-/_}
RESTORE_URL_VAR="RESTORE_URL_${HOSTNAME_SUFFIX^^}"
RESTORE_URL=${!RESTORE_URL_VAR}

# When downloading from S3, curl can succeed even if the object doesn't exist
# and also when a pre-signed URL is expired.
# In this case we get an XML file which can be detected with `file` so we show
# the contents via the logs then exit with an error status
curl -o /tmp/snapshot ${RESTORE_URL}
file /tmp/snapshot | grep -q XML && cat /tmp/snapshot && exit 1

# FIXME: etcdctl restore is deprecated but the etcd container doesn't have etcdutl
env ETCDCTL_API=3 /usr/bin/etcdctl -w table snapshot status /tmp/snapshot
env ETCDCTL_API=3 /usr/bin/etcdctl snapshot restore /tmp/snapshot --data-dir=/var/lib/data
