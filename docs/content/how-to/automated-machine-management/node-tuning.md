---
title: Node tuning
---

# Node tuning

Manage node-level tuning with the Node Tuning Operator.

## Creating a simple TuneD profile for setting sysctl settings
If you would like to set some node-level tuning on the nodes in your hosted cluster, you can use the [Node Tuning Operator](https://docs.openshift.com/container-platform/latest/scalability_and_performance/using-node-tuning-operator.html). In HyperShift, node tuning can be configured by creating ConfigMaps which contain Tuned objects, and referencing these ConfigMaps in your NodePools.

1. Create a ConfigMap which contains a valid Tuned manifest and reference it in a NodePool. The example Tuned manifest below defines a profile which sets `vm.dirty_ratio` to 55, on Nodes which contain the Node label `tuned-1-node-label` with any value.

    Save the ConfigMap manifest in a file called `tuned-1.yaml`:
    ```
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: tuned-1
      namespace: clusters
    data:
      tuning: |
        apiVersion: tuned.openshift.io/v1
        kind: Tuned
        metadata:
          name: tuned-1
          namespace: openshift-cluster-node-tuning-operator
        spec:
          profile:
          - data: |
              [main]
              summary=Custom OpenShift profile
              include=openshift-node

              [sysctl]
              vm.dirty_ratio="55"
            name: tuned-1-profile
          recommend:
          - priority: 20
            profile: tuned-1-profile
    ```
    > **_NOTE:_**  In the case where no labels are added to an entry in the `spec.recommend` section of the Tuned spec, NodePool based matching is assumed, so the highest priority profile in the `spec.recommend` section will be applied to Nodes in the pool. While more fine-grained Node label based matching is still possible by setting a label value in the Tuned `.spec.recommend.match`, users should be aware that Node labels will not persist during an upgrade, unless the NodePool `.spec.management.upgradeType` is set to `InPlace`.

    Create the ConfigMap in the management cluster:
    ```
    oc --kubeconfig="$MGMT_KUBECONFIG" create -f tuned-1.yaml
    ```

    Reference the ConfigMap in the NodePools `spec.tuningConfig` field, either by editing an existing NodePool or creating a new NodePool. In this example we assume we only have one NodePool called `nodepool-1`, containing 2 Nodes.
    ```
    apiVersion: hypershift.openshift.io/v1alpha1
    kind: NodePool
    metadata:
      ...
      name: nodepool-1
      namespace: clusters
    ...
    spec:
      ...
      tuningConfig:
      - name: tuned-1
    status:
    ...
    ```
    > **_NOTE:_**  You may reference the same ConfigMap in multiple NodePools. In HyperShift, NTO will append a hash of the NodePool name and namespace to the name of the Tuneds to distinguish them. Outside of this case, users should be careful not to create multiple Tuned profiles of the same name in different Tuneds for the same hosted cluster.

2. Now that the ConfigMap containing a Tuned manifest has been created and referenced in a NodePool, the Node Tuning Operator will sync the Tuned objects into the hosted cluster. You can check which Tuneds are defined and which profiles are set for each Node.

    List the Tuned objects in the hosted cluster:
    ```
    oc --kubeconfig="$HC_KUBECONFIG" get Tuneds -n openshift-cluster-node-tuning-operator
    ```

    Example output:
    ```
    NAME       AGE
    default    7m36s
    rendered   7m36s
    tuned-1    65s
    ```
   
    List the Profiles in the hosted cluster:
    ```
    oc --kubeconfig="$HC_KUBECONFIG" get Profiles -n openshift-cluster-node-tuning-operator
    ```

    Example output:
    ```
    NAME                           TUNED            APPLIED   DEGRADED   AGE
    nodepool-1-worker-1            tuned-1-profile  True      False      7m43s
    nodepool-1-worker-2            tuned-1-profile  True      False      7m14s
    ```

    As we can see, both worker nodes in the NodePool have the tuned-1-profile applied. Note that if no custom profiles are created, the `openshift-node` profile will be applied by default.


3. To confirm the tuning was applied correctly, we can start a debug shell on a Node and check the sysctl values:
    ```
    oc --kubeconfig="$HC_KUBECONFIG" debug node/nodepool-1-worker-1 -- chroot /host sysctl vm.dirty_ratio
    ```

    Example output:
    ```
    vm.dirty_ratio = 55
    ```

## Applying tuning which requires kernel boot parameters 
You can also use the Node Tuning Operator for more complex tuning which requires setting kernel boot parameters. 
As an example, the following steps can be followed to create a NodePool with huge pages reserved.

1. Create the following ConfigMap which contains a Tuned object manifest for creating 10 hugepages of size 2M.

    Save this ConfigMap manifest in a file called `tuned-hugepages.yaml`:
    ```
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: tuned-hugepages
      namespace: clusters
    data:
      tuning: |
        apiVersion: tuned.openshift.io/v1
        kind: Tuned
        metadata:
          name: hugepages
          namespace: openshift-cluster-node-tuning-operator
        spec:
          profile:
          - data: |
              [main]
              summary=Boot time configuration for hugepages
              include=openshift-node
              [bootloader]
              cmdline_openshift_node_hugepages=hugepagesz=2M hugepages=50
            name: openshift-node-hugepages
          recommend:
          - priority: 20
            profile: openshift-node-hugepages
    ```
    > **_NOTE:_**  The `.spec.recommend.match` field is intentionally left blank. In this case this Tuned will be applied to all Nodes in the NodePool where this ConfigMap is referenced. It is advised to group Nodes with the same hardware configuration into the same NodePool. Not following this practice might result in TuneD operands calculating conflicting kernel parameters for two or more nodes sharing the same NodePool.

    Create the ConfigMap in the management cluster:
    ```
    oc --kubeconfig="$MGMT_KUBECONFIG" create -f tuned-hugepages.yaml
    ```

2. Create a new NodePool manifest YAML file, customize the NodePools upgrade type, and reference the previously created ConfigMap in the `spec.tuningConfig` section before creating it in the management cluster.

    Create the NodePool manifest and save it in a file called `hugepages-nodepool.yaml`:
    ```
    NODEPOOL_NAME=hugepages-example
    INSTANCE_TYPE=m5.2xlarge
    NODEPOOL_REPLICAS=2

    hypershift create nodepool aws \
      --cluster-name $CLUSTER_NAME \
      --name $NODEPOOL_NAME \
      --replicas $NODEPOOL_REPLICAS \
      --instance-type $INSTANCE_TYPE \
      --render > hugepages-nodepool.yaml
    ```

    Edit `hugepages-nodepool.yaml`. Set `.spec.management.upgradeType` to `InPlace`, and set `.spec.tuningConfig` to reference the `tuned-hugepages` ConfigMap you created.
    ```
    apiVersion: hypershift.openshift.io/v1alpha1
    kind: NodePool
    metadata:
      name: hugepages-nodepool
      namespace: clusters
      ...
    spec:
      management:
        ...
        upgradeType: InPlace
      ...
      tuningConfig:
      - name: tuned-hugepages
    ```
    > **_NOTE:_**  Setting `.spec.management.upgradeType` to `InPlace` is recommended to avoid unnecessary Node recreations when applying the new MachineConfigs. With the `Replace` upgrade type, Nodes will be fully deleted and new nodes will replace them when applying the new kernel boot parameters that are calculated by the TuneD operand.

    Create the NodePool in the management cluster:
    ```
    oc --kubeconfig="$MGMT_KUBECONFIG" create -f hugepages-nodepool.yaml
    ```


3. After the Nodes become available, the containerized TuneD daemon will calculate the required kernel boot parameters based on the applied TuneD profile. After the Nodes become `Ready` and reboot once to apply the generated MachineConfig, you can verify that the Tuned profile is applied and that the kernel boot parameters have been set.

    List the Tuned objects in the hosted cluster:
    ```
    oc --kubeconfig="$HC_KUBECONFIG" get Tuneds -n openshift-cluster-node-tuning-operator
    ```

    Example output:
    ```
    NAME                 AGE
    default              123m
    hugepages-8dfb1fed   1m23s
    rendered             123m
    ```
   
    List the Profiles in the hosted cluster:
    ```
    oc --kubeconfig="$HC_KUBECONFIG" get Profiles -n openshift-cluster-node-tuning-operator
    ```

    Example output:
    ```
    NAME                           TUNED                      APPLIED   DEGRADED   AGE
    nodepool-1-worker-1            openshift-node             True      False      132m
    nodepool-1-worker-2            openshift-node             True      False      131m
    hugepages-nodepool-worker-1    openshift-node-hugepages   True      False      4m8s
    hugepages-nodepool-worker-2    openshift-node-hugepages   True      False      3m57s
    ```

    Both worker nodes in the new NodePool have the `openshift-node-hugepages` profile applied.


4. To confirm the tuning was applied correctly, we can start a debug shell on a Node and check `/proc/cmdline`
    ```
    oc --kubeconfig="$HC_KUBECONFIG" debug node/nodepool-1-worker-1 -- chroot /host cat /proc/cmdline
    ```

    Example output:
    ```
    BOOT_IMAGE=(hd0,gpt3)/ostree/rhcos-... hugepagesz=2M hugepages=50
    ```

## How to debug Node Tuning issues
If you face issues with Node Tuning, first check the Condition `ValidTuningConfig` in the NodePool that references your Tuned config. This reports any issue that may prevent the configuration load.
```
- lastTransitionTime: "2023-03-06T14:30:35Z"
  message: ConfigMap "tuned" not found
  observedGeneration: 2
  reason: ValidationFailed
  status: "False"
  type: ValidTuningConfig
```

If the NodePool condition shows no issues, it means that the configuration has been loaded and propagated to the NodePool. You can then check the status of the relevant `Profile` Custom Resource in your HostedCluster. In the conditions you should see if the configuration has been applied successfully and whether there are any outstanding Warning or Errors. An example can be seen below.
```
status:
  bootcmdline: ""
  conditions:
  - lastTransitionTime: "2023-03-06T14:22:14Z"
    message: The TuneD daemon profile not yet applied, or application failed.
    reason: Failed
    status: "False"
    type: Applied
  - lastTransitionTime: "2023-03-06T14:22:14Z"
    message: 'TuneD daemon issued one or more error message(s) during profile application.
      TuneD stderr:  ERROR    tuned.daemon.controller: Failed to reload TuneD: Cannot
      load profile(s) ''tuned-1-profile'': Cannot find profile ''openshift-node-notexistin''
      in ''[''/etc/tuned'', ''/usr/lib/tuned'']''.'
    reason: TunedError
    status: "True"
    type: Degraded
  tunedProfile: tuned-1-profile
```
