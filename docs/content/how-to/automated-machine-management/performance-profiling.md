# Manage node-level performance profiling with Performance Profile Controller

## Intro

The `Performance Profile controller`, formerly known as `Performance-Addon Operator`, is now part of `Node Tuning Operator`.
[Performance Profile controller](https://github.com/openshift/cluster-node-tuning-operator/blob/master/docs/performanceprofile/performance_controller.md) allows you to optimize nodes in your hosted cluster for applications sensitive to CPU and network latency.

In HyperShift, node profiling can be configured by creating ConfigMaps which contain [`PerformanceProfile`](https://docs.openshift.com/container-platform/latest/rest_api/node_apis/performanceprofile-performance-openshift-io-v2.html) objects, and referencing these ConfigMaps in your NodePools.

## Steps

1. Create a ConfigMap which contains a valid `PerformanceProfile` manifest and reference it in a NodePool.
   The example `PerformanceProfile` manifest defines several parameters like how to partition your CPUs into housekeeping and workload partitions, what topology policy to use, and more.
   Save this ConfigMap as `perfprof-1.yaml`.

    ```yaml
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: perfprof-1
      namespace: clusters
    data:
      tuning: |
        apiVersion: performance.openshift.io/v2
        kind: PerformanceProfile
        metadata:
          name: performance
        spec:
            additionalKernelArgs:
            - nmi_watchdog=0
            - audit=0
            - mce=off
            - processor.max_cstate=1
            - intel_idle.max_cstate=0
            - idle=poll
            cpu:
                isolated: "1,3,5,7,9,11,13,15,17,19-39,41,43,45,47,49,51,53,55,57"
                reserved: "0,2,4,6,8,10,12,14,16,18,40,42,44,46,48,50,52,54,56,58"
                offlined: "59-79"
            nodeSelector:
                node-role.kubernetes.io/worker-cnf: ""
            numa:
                topologyPolicy: single-numa-node
            realTimeKernel:
                enabled: true
            workloadHints:
                highPowerConsumption: true
                realTime: true
    ```

    > **_NOTE:_** See [PerformanceProfile Creator](https://docs.openshift.com/container-platform/latest/scalability_and_performance/cnf-create-performance-profiles.html) for help in creating this `PerformanceProfile` manifests.

2. Create the ConfigMap in the management cluster:

    ```bash
    oc --kubeconfig="$MGMT_KUBECONFIG" create -f perfprof-1.yaml
    ```

3. Reference the ConfigMap in the NodePools `spec.tuningConfig` field, either by editing an existing NodePool or creating a new NodePool. In this example we assume we only have one NodePool called `nodepool-1`, containing 2 Nodes.

    ```yaml
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
      - name: perfprof-1
    status:
    ...
    ```

    > **_NOTE:_**  Remember one NodePool can only reference at most one `PerformanceProfile`

4. Now that the ConfigMap containing a `PerformanceProfile` manifest has been created and referenced in a NodePool, the Performance Profile Controller will handle the `PerformanceProfile` object and will create the following elements:
    - `MachineConfig`: This will be embedded into a ConfigMap that will be handled by NTO as any other ConfigMap with MachineConfig embedded (see [this PR](https://github.com/openshift/hypershift/pull/1729) for further info)
    - `KubeletConfig`: This will be embedded into a ConfigMap that will be handled by NTO in a similar way as a ConfigMap with a MachineConfig embedded
    - `Tuned`: This will be embedded into a ConfigMap and so handled directly by the NTO.
    - `RuntimeClass`: This will be created directly inside the guest cluster.
