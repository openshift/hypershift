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

A PDB matching more than 2,000 pods makes the probe incomplete. The controller
sets the HCP `NodePoolDrainUpToDate` condition to `False` with reason
`PodScanIncomplete` and retries after one minute. The NodePool condition then
becomes `Unknown`; consumers should fail open. HCP status messages contain no
customer namespace or PDB names. Inspect HCCO logs for generic probe errors and
the guest cluster's PDB Events for blocking details. Management-cluster logs do
not contain customer namespace or PDB names.

Each HCP `status.nodePoolDrain` entry has a `state` of `Blocked` or `Drainable`.
The NodePool controller reports `Unknown` if that state is absent or unsupported,
which also handles status written by an older HCCO using the previous boolean
field. The NodePool `DrainBlocked` condition remains `True` for `Blocked` and
`False` for `Drainable`.
