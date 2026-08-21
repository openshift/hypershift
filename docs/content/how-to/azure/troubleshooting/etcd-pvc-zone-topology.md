# Etcd PVC Pending Due to Zone Topology Mismatch

## Symptom

The HostedCluster's `EtcdAvailable` condition is `False` with reason `EtcdPVCPending` and a message referencing the StorageClass and zone-aware provisioning:

```
etcd PVC data-etcd-0 has been pending for 10m (StorageClass "premiumv2-csi"):
If the StorageClass requires zone-aware provisioning, ensure management cluster
nodes are deployed into availability zones with valid topology labels
```

## Cause

Some Azure StorageClasses, such as `PremiumV2_LRS`, require zone-aware provisioning. The Azure Disk CSI driver validates that nodes have a region-qualified zone label (for example, `centralus-1`) in `topology.disk.csi.azure.com/zone` before provisioning a disk.

When the management cluster's nodes were **not** deployed into availability zones (that is, without `--availability-zones`), `cloud-provider-azure` falls back to `platformFaultDomain` when labelling `topology.kubernetes.io/zone`. This produces a bare, 0-based value (for example, `0`, `1`, `2`), which `IsValidAvailabilityZone()` in the Azure Disk CSI driver rejects. The CSI zone label ends up empty, and the PVC cannot bind.

## Resolution

Ensure that the management cluster's nodes are deployed into availability zones. For AKS, specify availability zones when creating the node pool:

```sh
az aks nodepool add \
  --resource-group <resource-group> \
  --cluster-name <cluster-name> \
  --name <nodepool-name> \
  --zones 1 2 3 \
  ...
```

Verify that nodes carry region-qualified zone labels:

```sh
kubectl get nodes -o custom-columns=\
NAME:.metadata.name,\
ZONE:'.metadata.labels.topology\.kubernetes\.io/zone',\
CSIZONE:'.metadata.labels.topology\.disk\.csi\.azure\.com/zone'
```

Expected output shows values like `centralus-1`, not bare numbers like `0`:

```
NAME              ZONE           CSIZONE
aks-nodepool-0    centralus-1    centralus-1
aks-nodepool-1    centralus-2    centralus-2
```

## Notes

- `AzureNodePoolPlatform.availabilityZone` is optional because some Azure regions do not support availability zones. Non-zonal configurations are valid, but they are incompatible with zone-aware StorageClasses.
- The `spec.etcd.managed.storage.persistentVolume.storageClassName` field accepts any StorageClass name. HyperShift cannot validate the StorageClass against the management cluster's topology at admission time because the CPO runs in a namespace-scoped context.
