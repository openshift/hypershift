A `NodePool` is a scalable set of worker nodes associated with a HostedCluster. NodePool machine architectures remain consistent within a specific pool and are independent of the underlying machine architecture of the control plane.

!!! note

    Please ensure you modify the appropriate fields to align with your laboratory environment.

!!! warning

    Before a day-1 patch, the release image set in the HostedCluster **should use the digest rather than the tag**. (e.g `quay.io/openshift-release-dev/ocp-release@sha256:e3ba11bd1e5e8ea5a0b36a75791c90f29afb0fdbe4125be4e48f69c76a5c47a0`)

This is how one looks like:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  creationTimestamp: null
  name: hosted-dual
  namespace: clusters
spec:
  arch: amd64
  clusterName: hosted-dual
  management:
    autoRepair: false
    upgradeType: InPlace
  nodeDrainTimeout: 0s
  nodeVolumeDetachTimeout: 0s
  platform:
    type: Agent
  release:
    image: registry.hypershiftbm.lab:5000/openshift/release-images:4.14.0-0.nightly-2023-08-29-102237
  replicas: 0
status:
  replicas: 0
```

**Details**:

- All the nodes included in this NodePool will be based on the Openshift version `4.14.0-0.nightly-2023-08-29-102237`.
- The Upgrade type is set to `InPlace`, indicating that the same bare-metal node will be reused during an upgrade.
- Autorepair is set to `false` because the node will not be recreated when it disappears.
- Replicas are set to `0` because we intend to scale them when needed.

You can find more information about NodePool in the [NodePool documentation](https://hypershift.pages.dev/reference/api/#hypershift.openshift.io%2fv1beta1).

To deploy this object, simply follow the same procedure as before:

```bash
oc apply -f 02-nodepool.yaml
```

And this is how the NodePool looks like at this point:

```bash
NAMESPACE   NAME          CLUSTER   DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION                              UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
clusters    hosted-dual   hosted    0                               False         False        4.14.0-0.nightly-2023-08-29-102237
```

!!! important

    Keep the nodepool replicas to 0 until all the steps are in place.