# Diagnose or observe teardown

Use when deletion has already begun or cleanup is part of the authorized task.
First retain HC UID, infrastructure ID, worker VPC or resource-group ownership,
provider identities, published endpoint resource identities and whether the
HC retention annotation is set to `true`; record the annotation before deletion
when possible. Keep identifiers in access-controlled run metadata; omit
endpoint hosts and URLs from shared evidence unless explicitly required and
approved. If starting deletion, inventory before invoking the normal supported
destroy workflow.
Do not infer permission to destroy from a startup-debugging request.

Follow the first remaining dependency, allowing cleanup workstreams to overlap:

| Remaining resource | Inspect first |
|--------------------|---------------|
| NodePool/Machine | Deleting conditions, infrastructure-machine finalizers, CAPA/CAPZ logs and instance/VM state |
| CAPI/provider Cluster | Remaining Machines, provider conditions and exact cloud errors |
| HCP | CPO cleanup, guest cloud resources, Services/PVCs and endpoint cleanup |
| HCP namespace/pods | Check the HC retention annotation; otherwise inspect remaining objects, namespace conditions, finalizers and pod grace periods |
| HC after HCP namespace removal | Inspect `metadata.finalizers` for `openshift.io/destroy-cluster`, destroy command progress and remaining cloud/IAM resources |
| Failed query | Authentication, permissions, discovery or connectivity; absence is unverified |

An HCP namespace may be intentionally retained. The HC annotation
`hypershift.openshift.io/skip-delete-hosted-controlplane-namespace: "true"`
instructs HyperShift to keep it during HC deletion. Record the value before
deletion or inspect the HC while it still exists. When it is `true`, inspect
remaining objects and report the namespace as intentionally retained within
the requested cleanup scope. If the HC is gone and the value was not recorded,
treat retention intent as unknown; namespace presence alone does not establish
a cleanup failure or success.

The destroy finalizer is `openshift.io/destroy-cluster`. Inspect it in
`metadata.finalizers` when HC deletion is blocked. HyperShift adds it only when
platform-specific cleanup is configured, so deletion through another path may
not include it. Its absence alone does not verify that owned cloud resources
have been removed.

An AWS Machine can remain in `WaitingForInfrastructureDeletion` while EC2
shuts down. A terminating pod's `deletionTimestamp` can be a future grace
deadline. Retain deletion/termination grace-period fields and first observed
termination separately from first absence. Namespace conditions can lag or
omit what a separate object query still sees; inspect both.

On AWS, temporary internet-gateway `DependencyViolation` errors for mapped
public addresses can accompany a NAT gateway still `deleting`. Watch the
exact gateway and its dependencies for progress before treating a retry as
a permanent leak. A terminal `deleted` NAT record or `terminated` EC2 record
can remain visible after cleanup; report the terminal state accurately.

For timing, use `hack/cluster-teardown-timeline.py` only if it exists in the
selected HyperShift checkout and its `--help` confirms it fits the platform
and scope. If an AWS collector is available, it requires a pre-deletion
inventory, exact HC UID/infra/VPC/OIDC inputs and a command-state file. Its
NodePool inventory covers the whole HC namespace, so use it only with a
dedicated HC namespace or adapt filtering first. It does not implement Azure
cloud cleanup. When the script is absent or unsuitable, use scoped timestamped
snapshots instead.

Distinguish successful destroy-command return from independent cleanup checks.
Verify the HC, its selected NodePools and relevant CAPI resources are absent.
Require the HCP namespace to be absent only if the HC retention annotation was
not `true`. When it was `true`, inspect retained objects and report the
namespace as intentionally retained within the requested cleanup scope. If the
annotation value is unknown, report retention intent as unverified. Then verify
the exact owned cloud/network/IAM resources. Track detached EIPs, volumes,
gateways, load balancers/target groups and private zones
by retained identity so detachment alone is not counted as deletion. For Azure,
respect shared resource groups and verify exact owned resources; do not delete
a whole resource group merely to clear one cluster.

Do not remove finalizers or force-delete to improve a timing sample. If normal
cleanup fails, save the concrete blocker and propose a scoped remedy. Shared
management infrastructure, HO, base-domain zones and OIDC buckets can remain
outside the cluster's deletion scope; name that scope in the cleanup result.
