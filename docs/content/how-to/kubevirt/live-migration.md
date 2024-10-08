# Worker Node Live Migration Overview

In order to prevent disrupting guest cluster workloads, guest cluster VMs are
automatically live migrated during update and maintenance events occurring on
the underlying infrastructure cluster hosting the VMs.

This functionality allows the underlying infrastructure cluster to update
without impacting the availability and operation of the KubeVirt platform guest
clusters

## Verifying Live Migration

Live migration of the KubeVirt VMs is enabled by default as long as the VMs
meet the necessary requirements. The primary requirement is that the VMs must
use ReadWriteMany (RWX) storage for both the root volume and the storage classes
mapped to kubevirt-csi.

It is possible to verify VMs within a NodePool are capable of live migration by
inspecting the `KubeVirtNodesLiveMigratable` condition within the NodePool's
status section.

Below is an example of a NodePool condition with VMs which are unable to live
migrate due to not using RWX shared storage.

```yaml
    - lastTransitionTime: "2024-10-08T15:38:19Z"
      message: |
        3 of 3 machines are not live migratable
        Machine vossel-np-ngst4-gw2hz: DisksNotLiveMigratable: vossel-np-ngst4-gw2hz is not a live migratable machine: cannot migrate VMI: PVC vossel-np-ngst4-gw2hz-rhcos is not shared, live migration requires that all PVCs must be shared (using ReadWriteMany access mode)
        Machine vossel-np-ngst4-npq7x: DisksNotLiveMigratable: vossel-np-ngst4-npq7x is not a live migratable machine: cannot migrate VMI: PVC vossel-np-ngst4-npq7x-rhcos is not shared, live migration requires that all PVCs must be shared (using ReadWriteMany access mode)
        Machine vossel-np-ngst4-q5nkb: DisksNotLiveMigratable: vossel-np-ngst4-q5nkb is not a live migratable machine: cannot migrate VMI: PVC vossel-np-ngst4-q5nkb-rhcos is not shared, live migration requires that all PVCs must be shared (using ReadWriteMany access mode)
      observedGeneration: 1
      reason: DisksNotLiveMigratable
      status: "False"
      type: KubeVirtNodesLiveMigratable
```

And here is the NodePool condition indicating that the VMs are live migratable.

```
    - lastTransitionTime: "2024-10-08T15:38:19Z"
      message: "All is well"
      observedGeneration: 1
      reason: AsExpected
      status: "True"
      type: KubeVirtNodesLiveMigratable
```

## Unavoidable Workload Disruption

While live migration will protect live migratable VMs from disruption under
normal operation, events such as infrastructure node failure will result in
a hard restart of any VMs hosted on the failed node. Live migration requires
the source node that a VMs is hosted on to be healthy before the VM can migrate.

## Expections when Live Migration is Disabled

When VMs within a NodePool are not capable of live migrating, workload
disruption on the guest cluster can occur during infrastructure cluster
maintenance events. By default during normal operation the hypershift
controllers will attempt to drain the workloads hosted on KubeVirt VMs not
capable of live migration before the VM is shutdown. Draining the guest cluster
nodes before shutting the VMs down allows PodDisruptionBudgets to help protect
workload availability within the guest cluster.
