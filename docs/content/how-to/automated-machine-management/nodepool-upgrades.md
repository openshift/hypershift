---
title: NodePool Upgrades
---

# NodePool Upgrades

NodePools enable the ability to express intent to configure the software running in the Nodes to a certain level by exposing `spec.release` and `spec.config`. Changing any of these fields will result in a new payload generation rolled out across all Nodes belonging to a NodePool, i.e. NodePool upgrade.

Changes to any platform specific field, e.g. aws instance type, will also cause a rolling upgrade, which will result on a set of new instances with the new type. As well, some cluster config changes (e.g. proxy, certs) may also trigger a rolling upgrade if the change propagates to the node.

NodePools support two types of upgrades: Replace and InPlace, specified via [UpgradeType](../../reference/api.md#hypershift.openshift.io/v1alpha1.UpgradeType).

!!! important

    You cannot switch the UpgradeType once the NodePool is created. You must specify UpgradeType during NodePool 
    creation. Modifying the field after the fact may cause nodes to become unmanaged.

### Replace Upgrades

This will create new instances in the new version while removing old nodes in a rolling fashion. This is usually a good choice in cloud environments where this level of immutability is cost effective.

### InPlace Upgrades

This will directly perform updates to the Operating System of the existing instances. This is usually a good choice for environments where the infrastructure constraints are higher e.g. bare metal.

### Triggering Upgrades

#### Upgrading to a new OCP version

These upgrades can be triggered via changing the `spec.release.image` of the NodePool. Note that you should only upgrade NodePools to the current version of the Hosted Control Plane.

#### Adding a new MachineConfig

You can create a MachineConfig inside a ConfigMap in the management cluster as follows:

```
apiVersion: v1
kind: ConfigMap
metadata:
  name: ${CONFIGMAP_NAME}
  namespace: clusters
data:
  config: |
    apiVersion: machineconfiguration.openshift.io/v1
    kind: MachineConfig
    metadata:
      labels:
        machineconfiguration.openshift.io/role: worker
      name: ${MACHINECONFIG_NAME}
    spec:
      config:
        ignition:
          version: 3.2.0
        storage:
          files:
          - contents:
              source: data:...
            mode: 420
            overwrite: true
            path: ${PATH}
```

Once that is applied to the cluster, you can specify that to the NodePool via:

```
spec:
  config:
    - name: ${CONFIGMAP_NAME}
```
