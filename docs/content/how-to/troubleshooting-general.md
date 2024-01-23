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

## Troubleshoot By Provider
If you have provider-scoped questions, please take a look at the troubleshooting section for the provider in the list below.
We will keep adding more and more troubleshooting sections and updating the existent ones.

- [AWS](./aws/troubleshooting/index.md)
- [Azure](./azure/troubleshooting/index.md)