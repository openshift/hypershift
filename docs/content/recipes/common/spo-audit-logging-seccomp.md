# Configuring Security Profiles Operator (SPO) in Hosted Control Planes

The [Security Profiles Operator (SPO)](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/security_and_compliance/security-profiles-operator) enables capturing `exec`, `rsh`, and `debug` sessions using advanced audit logging combined with seccomp log mode. The standard OCP documentation assumes a self-managed cluster where node-level components like CRI-O can be configured directly (e.g., `--privileged-seccomp-profile`). In a Hosted Control Plane (HCP) environment, the control plane and worker infrastructure are separated, so the configuration steps differ.

This recipe covers the HCP-specific steps required to set up SPO with audit logging and seccomp log mode.

## Prerequisites

- A running HostedCluster managed by HyperShift
- `oc` CLI with access to both the management cluster and the hosted cluster
- Familiarity with the [SPO documentation for standard OCP](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/security_and_compliance/security-profiles-operator)

## Step 1: Configure the Audit Log Profile

SPO requires an audit log profile that captures request bodies (e.g., `WriteRequestBodies` or `AllRequestBodies`). In HCP, the Kubernetes API Server (KAS) configuration is managed through the HostedCluster resource on the management cluster.

Patch the HostedCluster to set the desired audit profile:

```bash
oc patch hostedcluster <hosted_cluster_name> \
  -n <hosted_cluster_namespace> \
  --type=merge \
  -p '{"spec": {"configuration": {"apiServer": {"audit": {"profile": "AllRequestBodies"}}}}}'
```

!!! note
    Replace `<hosted_cluster_name>` and `<hosted_cluster_namespace>` with the name and namespace of your HostedCluster resource on the management cluster.

!!! tip
    You can also use `WriteRequestBodies` if you only need to capture write operations. `AllRequestBodies` captures both read and write request bodies and generates more log data.

After patching, the control plane operator will roll out the KAS pods with the updated audit configuration. You can monitor the rollout:

```bash
oc get pods -n <hosted_control_plane_namespace> -l app=kube-apiserver -w
```

## Step 2: Understanding Audit Log Location in HCP

In a standard OCP cluster, audit logs are stored on the control plane nodes and are directly accessible by components running on the same cluster. In HCP, the architecture is fundamentally different: the KAS runs as pods on the **management cluster**, while SPO runs on the **hosted (guest) cluster**.

!!! warning "Key Architectural Difference"
    The KAS audit logs are **not accessible from the guest cluster**. The KAS pods reside in the HostedControlPlane namespace on the management cluster, and there is no direct path from the guest cluster's data plane to those logs. SPO, running on the guest cluster, cannot natively read the KAS audit logs the way it does on a standard OCP cluster.

### Viewing Audit Logs from the Management Cluster

An administrator with access to the management cluster can view the audit logs directly:

```bash
oc get pods -n <hosted_control_plane_namespace> -l app=kube-apiserver
```

```bash
oc logs -n <hosted_control_plane_namespace> <kas_pod_name> -c kube-apiserver | grep audit
```

!!! note
    The `<hosted_control_plane_namespace>` is typically `<hosted_cluster_namespace>-<hosted_cluster_name>` on the management cluster.

### Making Audit Logs Available to SPO

Since SPO on the guest cluster cannot directly access the KAS audit logs on the management cluster, you need to establish a mechanism to forward or expose those logs. Some approaches to consider:

- **Log forwarding via ClusterLogForwarder**: Configure log forwarding on the management cluster to send KAS audit logs to a centralized logging backend (e.g., Elasticsearch, Loki, Splunk). SPO and security teams can then consume audit data from the shared logging infrastructure.
- **Audit Log Persistence with external access**: Enable the [Audit Log Persistence](../../how-to/audit-log-persistence.md) feature to store audit logs in PersistentVolumes on the management cluster, then export or sync them to a location accessible to the guest cluster or to your security tooling.
- **Audit webhook backend**: Configure the KAS audit webhook backend to send audit events to an external endpoint that SPO or your security infrastructure can consume. This can be configured via the HostedCluster's `spec.configuration.apiServer.audit` settings.

!!! tip
    The specific approach depends on your organization's logging architecture and security requirements. In all cases, the audit logs must be forwarded or exported from the management cluster since the guest cluster has no direct access to the KAS pods.

## Step 3: Configure Worker Nodes for Seccomp Logging

SPO requires CRI-O configuration on worker nodes to enable the `--privileged-seccomp-profile` or seccomp log mode. In HCP, worker node configuration is applied via MachineConfig through the NodePool.

### Creating the MachineConfig for Seccomp

- Create a CRI-O configuration file that enables the seccomp log annotation:

```bash
cat <<EOF > crio-seccomp-config.conf
[crio.runtime]
seccomp_use_default_when_empty = false

[crio.runtime.runtimes.runc]
allowed_annotations = [
    "io.containers.trace-syscall",
]
EOF
```

- Get the base64 encoding of the file content:

```bash
export SECCOMP_CONFIG_HASH=$(cat crio-seccomp-config.conf | base64 -w0)
```

- Create the MachineConfig manifest:

```bash
cat <<EOF > mc-seccomp-logging.yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 60-seccomp-logging
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
          source: data:text/plain;charset=utf-8;base64,${SECCOMP_CONFIG_HASH}
        mode: 420
        path: /etc/crio/crio.conf.d/99-seccomp-logging.conf
EOF
```

- Create a ConfigMap containing the MachineConfig in the HostedCluster namespace:

```bash
oc create -n <hosted_cluster_namespace> configmap mcp-seccomp-logging \
  --from-file config=mc-seccomp-logging.yaml
```

- Patch the NodePool to apply the MachineConfig:

```bash
oc patch -n <hosted_cluster_namespace> nodepool <nodepool_name> \
  --type=json \
  -p='[{"op": "add", "path": "/spec/config", "value": [{"name": "mcp-seccomp-logging"}]}]'
```

!!! warning
    If your NodePool already has existing config entries in `/spec/config`, use `"op": "add", "path": "/spec/config/-"` instead to append rather than replace the existing configuration.

!!! note
    After patching the NodePool, worker nodes will be rolled out with the new CRI-O configuration. This may cause temporary disruption as nodes are drained and replaced. For more details on MachineConfig management in HCP, see the [Configure Machines](../../how-to/automated-machine-management/configure-machines.md) documentation.

## Step 4: Install and Configure SPO

Once the audit profile and worker node seccomp configuration are in place, install and configure the Security Profiles Operator on the hosted cluster following the [standard SPO installation guide](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/security_and_compliance/security-profiles-operator#installing-the-security-profiles-operator_security-profiles-operator).

The SPO installation itself is the same as on a standard OCP cluster since it runs on the hosted cluster's data plane.

## Summary of Differences from Standard OCP

| Configuration | Standard OCP | HCP |
|---|---|---|
| Audit log profile | Configured via `openshift-kube-apiserver` operator | Patch HostedCluster resource on management cluster |
| Audit log location | Control plane node filesystem (accessible by SPO) | KAS pod logs in HostedControlPlane namespace on management cluster (**not accessible from guest cluster**) |
| Audit log access for SPO | Direct access on the same cluster | Requires log forwarding, audit webhook, or external export from management cluster |
| CRI-O seccomp config | MachineConfigPool on cluster nodes | MachineConfig via ConfigMap + NodePool patch |
| SPO installation | Standard OLM install | Same (runs on hosted cluster data plane) |

## Related Documentation

- [Audit Log Persistence](../../how-to/audit-log-persistence.md) - Persistent storage for KAS audit logs in HCP
- [Configure Machines](../../how-to/automated-machine-management/configure-machines.md) - Applying MachineConfig via NodePool
- [Replace CRI-O Runtime](./replace-crio-runtime.md) - Another recipe for CRI-O configuration in HCP
- [OCP Security Profiles Operator Documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/security_and_compliance/security-profiles-operator) - Full SPO reference
