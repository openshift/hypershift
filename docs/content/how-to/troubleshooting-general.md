# Troubleshooting

## General
### Dump HostedCluster resources from a management cluster
To dump the relevant HostedCluster objects, we will need some prerequisites:

- `cluster-admin` access to the management cluster
- The HostedCluster `name` and the `namespace` where the CR is deployed
- The Hypershift CLI
- The Openshift CLI

Once we have these elements and the shell with the Kubeconfig loaded pointing to the management cluster, we will execute these commands:

```bash
CLUSTERNAME="samplecluster"
CLUSTERNS="clusters"

mkdir clusterDump-${CLUSTERNS}-${CLUSTERNAME}
hypershift dump cluster \
    --name ${CLUSTERNAME} \
    --namespace ${CLUSTERNS} \
    --dump-guest-cluster \
    --artifact-dir clusterDump-${CLUSTERNS}-${CLUSTERNAME}
```

After some time, the output will show something like this:

```bash
2023-06-06T12:18:20+02:00	INFO	Archiving dump	{"command": "tar", "args": ["-cvzf", "hypershift-dump.tar.gz", "cluster-scoped-resources", "event-filter.html", "namespaces", "network_logs", "timestamp"]}
2023-06-06T12:18:21+02:00	INFO	Successfully archived dump	{"duration": "1.519376292s"}
```

This dump contains artifacts that aid in troubleshooting issues with hosted control plane clusters.

#### Contents from Dump Command
The Management's Cluster's dump content:

- **Cluster scoped resources**: Basically nodes definitions of the management cluster.
- **The dump compressed file**: This is useful if you need to share the dump with other people
- **Namespaced resources**: This includes all the objects from all the relevant namespaces, like configmaps, services, events, logs, etc...
- **Network logs**: Includes the OVN northbound and southbound DBs and the statuses for each one.
- **HostedClusters**: Another level of dump, involves all the resources inside of the guest cluster.

The Guest Cluster dump content:

- **Cluster scoped resources**: It contains al the cluster-wide objects, things like nodes, CRDs, etc...
- **Namespaced resources**: This includes all the objects from all the relevant namespaces, like configmaps, services, events, logs, etc...

!!! note

    **The dump will not contain any Secret object** from the cluster, only references to the secret's names.

#### Impersonation as user/service account

The dump command can be used with the flag `--as`, which works in the same way as the `oc` client. If you execute the command with the flag, the CLI will impersonate all the queries against the management cluster, using that username or service account.

The service account should have enough permissions to query all the objects from the namespaces, so cluster-admin is recommended to make sure you have enough permissions. The service account should be located (or have permissions to query) at least the HostedControlPlane namespace.

!!! note

    If your user/sa doesn't have enough permissions, the command will be executed and will dump only the objects you have permissions to get and during that process some `forbidden` errors will be raised.

- Impersonation Sample using a service account:

```bash
CLUSTERNAME="samplecluster"
CLUSTERNS="clusters"
SA="samplesa"
SA_NAMESPACE="default"

mkdir clusterDump-${CLUSTERNS}-${CLUSTERNAME}
hypershift dump cluster \
    --name ${CLUSTERNAME} \
    --namespace ${CLUSTERNS} \
    --dump-guest-cluster \
    --as "system:serviceaccount:${SA_NAMESPACE}:${SA}" \
    --artifact-dir clusterDump-${CLUSTERNS}-${CLUSTERNAME}
```

- Impersonation Sample using a user:

```bash
CLUSTERNAME="samplecluster"
CLUSTERNS="clusters"
CLUSTERUSER="cloud-admin"

mkdir clusterDump-${CLUSTERNS}-${CLUSTERNAME}
hypershift dump cluster \
    --name ${CLUSTERNAME} \
    --namespace ${CLUSTERNS} \
    --dump-guest-cluster \
    --as "${CLUSTERUSER}" \
    --artifact-dir clusterDump-${CLUSTERNS}-${CLUSTERNAME}
```

### How to view the ignition payload

1. Define the HCP namespace where the user-data secret is stored
```shell 
HCP_NAMESPACE="<hcp-namespace>"
```
1. Find the user-data secret in the HCP namespace
```shell
SECRET_NAME=$(oc get secret -n $HCP_NAMESPACE | grep user-data | awk '{print $1}')
```
2. Retrieve the secret and decode the value key
```shell
USER_DATA_VALUE=$(oc get secret $SECRET_NAME -n $HCP_NAMESPACE -o jsonpath='{.data.value}' | base64 -d)
```
3. Extract the bearer token and ignition server from the user-data value
```shell
BEARER_TOKEN=$(echo $USER_DATA_VALUE | jq -r '.ignition.config.merge[0].httpHeaders[] | select(.name=="Authorization") | .value')
IGNITION_SERVER=$(echo $USER_DATA_VALUE | jq -r '.ignition.config.merge[0].source')
```

4. Download the ignition payload from the ignition-server and save it to a file
```shell
curl -k -H "Authorization: $BEARER_TOKEN" $IGNITION_SERVER -o ignition.json
```

#### How to view the files in the ignition payload
Following on from the previous section and the example file in step 4, `ignition.json`, to view the files within the payload, execute the following command:
```
% cat ignition.json | jq '.storage.files[].path'
"/usr/local/bin/nm-clean-initrd-state.sh"
"/etc/NetworkManager/conf.d/01-ipv6.conf"
"/etc/NetworkManager/conf.d/20-keyfiles.conf"
"/etc/pki/ca-trust/source/anchors/openshift-config-user-ca-bundle.crt"
"/etc/kubernetes/apiserver-url.env"
"/etc/audit/rules.d/mco-audit-quiet-containers.rules"
"/etc/tmpfiles.d/cleanup-cni.conf"
"/usr/local/bin/configure-ovs.sh"
"/etc/containers/storage.conf"
"/etc/mco/proxy.env"
...
```

To view the specific contents of a file, execute the following command:
```
% cat ignition.json | jq '.storage.files[] | select(.path=="/etc/kubernetes/apiserver-url.env")'
{
  "overwrite": true,
  "path": "/etc/kubernetes/apiserver-url.env",
  "contents": {
    "source": "data:,KUBERNETES_SERVICE_HOST%3D'52.150.32.156'%0AKUBERNETES_SERVICE_PORT%3D'7443'%0A"
  },
  "mode": 420
}
```

## Troubleshoot By Provider
If you have provider-scoped questions, please take a look at the troubleshooting section for the provider in the list below.
We will keep adding more and more troubleshooting sections and updating the existent ones.

- [AWS](./aws/troubleshooting/index.md)
- [Azure](./azure/troubleshooting/index.md)