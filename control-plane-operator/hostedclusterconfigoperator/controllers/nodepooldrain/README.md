# NodePool drain readiness

Set `ENABLE_NODEPOOL_DRAIN_READINESS=1` on the HyperShift operator deployment to
make the signal available. This is a global capability switch; it does not enable
the controller for every HostedCluster. Opt a trial cluster in by setting
`hypershift.openshift.io/enable-nodepool-drain-readiness=true` on its
HostedCluster. The setting is mirrored to its HostedControlPlane and propagated
through that cluster's control plane operator to HCCO. Removing the annotation
disables the signal for that cluster, and the NodePool controller removes any
`DrainBlocked` condition left by an earlier enabled run.

CPO is normally selected from the HostedCluster's OpenShift release payload. For
a POC using this branch, build and pin a CPO image containing the controller by
setting the existing `hypershift.openshift.io/control-plane-operator-image`
annotation on the trial HostedCluster. Deploy the HyperShift operator image from
this branch and set the capability switch above.

For example, after building and pushing the branch CPO image:

```sh
kubectl annotate hostedcluster -n <hosted-cluster-namespace> <hosted-cluster-name> \
  hypershift.openshift.io/enable-nodepool-drain-readiness=true \
  hypershift.openshift.io/control-plane-operator-image=<branch-cpo-image>
```

The controller excludes the `openshift-` and `kube-` namespace prefixes, plus
the exact namespaces `openshift` and `kube-system`. The `default` namespace is
treated as a customer namespace. Managed deployments can add exclusions by
setting `NODEPOOL_DRAIN_ADDITIONAL_PLATFORM_NAMESPACE_PREFIXES` on the HyperShift
operator deployment to a comma-separated list, for example
`redhat-,open-cluster-management`. Empty and repeated entries are ignored. This
setting also propagates to the hosted cluster config operator for opted-in
clusters. Pod placement is not watched; a successful probe repeats every minute
to bound how long a move between pools can leave the signal unchanged.

The probe is incomplete if a matching PDB selects more than 2,000 pods, the
aggregate scan reaches 10,000 returned pods or 100 uncached Pod-list requests
before all blocking PDBs are checked, any Pod list exceeds its 30-second
timeout, or the full scan exceeds two minutes. If a relevant customer PDB's
status has not observed its current spec generation, the probe
also fails open instead of reporting `Drainable`. In each case the HCP
`NodePoolDrainUpToDate` condition becomes `False` and the controller retries
after one minute. The NodePool condition then becomes `Unknown`.

HCP status contains NodePool names and UIDs because results are keyed by pool.
It does not contain customer namespaces, PDB names, or Pod names. Events and
management-cluster logs contain no customer or NodePool identifiers. A guest
`DrainBlocked` Event is generic; inspect PDBs in the guest cluster to identify
the workload. HCCO logs contain only a fixed reason and count.

Each HCP `status.nodePoolDrain` entry has a `state` of `Blocked` or `Drainable`
and a NodePool UID. HCCO matches guest Nodes to CAPI Machines tagged with that
UID. New Machine templates carry the UID; existing Machines are tagged in place
without rolling the pool. A pool with orphaned or mixed-incarnation Nodes has no
entry. The NodePool controller reports `Unknown` when the UID does not match
the current NodePool, or when the state is absent or unsupported. This also
handles status written by an older HCCO. The NodePool `DrainBlocked`
condition remains `True` for `Blocked` and `False` for `Drainable`.
