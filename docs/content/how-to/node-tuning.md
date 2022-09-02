# Manage node-level tuning with the Node Tuning Operator

If you would like to set some node-level tuning on the nodes in your hosted cluster, you can use the [Node Tuning Operator](https://docs.openshift.com/container-platform/4.11/scalability_and_performance/using-node-tuning-operator.html). In HyperShift, node tuning can be configured by creating ConfigMaps which contain Tuned objects, and referencing these ConfigMaps in your NodePools. Currently Node Tuning is limited to tunables which the TuneD daemon can apply directly like setting `sysctl` values. Tuning that requires setting kernel boot parameters is not yet supported in HyperShift.

1. Create a ConfigMap which contains a valid Tuned manifest and reference it in a NodePool. The example Tuned manifest below defines a profile which sets `vm.dirty_ratio` to 55, on Nodes which contain the Node label  `tuned-1-node-label` with any value. 

    Save the ConfigMap manifest in a file called `tuned-1.yaml`:
    ```
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: tuned-1
      namespace: clusters
    data:
      tuned: |
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

    Reference the ConfigMap in the NodePools `spec.tunedConfig` field, either by editing an existing NodePool or creating a new NodePool. In this example we assume we only have one NodePool called `nodepool-1`, containing 2 Nodes.
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
      tunedConfig:
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

    As we can see, both worker nodes in the nodepool have the tuned-1-profile applied. Note that if no custom profiles are created, the `openshift-node` profile will be applied by default.


3. To confirm the tuning was applied correctly, we can start a debug shell on a Node and check the sysctl values:
    ```
    oc --kubeconfig="$HC_KUBECONFIG" debug node/nodepool-1-worker-1 -- chroot /host sysctl vm.dirty_ratio
    ```

    Example output:
    ```
    vm.dirty_ratio = 55
    ```