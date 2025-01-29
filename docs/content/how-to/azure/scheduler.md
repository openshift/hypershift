# Azure Scheduler

The Azure Scheduler works with the default `ClusterSizingConfiguration` resource and the `HostedClusterSizing` controller.

## ClusterSizingConfiguration

The `ClusterSizingConfiguration` is an API used for setting tshirt sizes based on the number of nodes a `HostedCluster` has. Each tshirt size can configure different effects that control various aspects of the cluster, such as the Kube API Server (KAS), etcd, etc. Additionally, it allows controlling the frequency of transitions between cluster sizes.

### Effects

- `kasGoMemLimit`: Specifies the memory limit for the Kube API Server.
- `controlPlanePriorityClassName`: The priority class for most control plane pods.
- `etcdPriorityClassName`: The priority class for etcd pods.
- `apiCriticalPriorityClassName`: The priority class for pods in the API request serving path, including Kube API Server and OpenShift APIServer.
- `resourceRequests`: Allows specifying resource requests for control plane pods.
- `machineHealthCheckTimeout`: Specifies an optional timeout for machine health checks created for `HostedClusters` with this specific size.
- `maximumRequestsInFlight`: Specifies the maximum requests in flight for Kube API Server.
- `maximumMutatingRequestsInflight`: Specifies the maximum mutating requests in flight for Kube API Server.

### ConcurrencyConfiguration

The `ConcurrencyConfiguration` defines the bounds of allowed behavior for clusters transitioning between sizes. It includes:

- `SlidingWindow`: The window over which the concurrency bound is enforced. This is a duration (e.g., `10m` for 10 minutes) that specifies the time frame within which the concurrency limit is applied.
- `Limit`: The maximum allowed number of cluster size transitions during the sliding window. This is an integer (e.g., `5`) that specifies how many transitions can occur within the sliding window.

### TransitionDelayConfiguration

The `TransitionDelayConfiguration` defines the lag between cluster size changing and the assigned tshirt size class being applied. It includes:

- `Increase`: The minimum period of time to wait between a cluster's size increasing and the tshirt size assigned to it being updated to reflect the new size. This is a duration (e.g., `30s` for 30 seconds).
- `Decrease`: The minimum period of time to wait between a cluster's size decreasing and the tshirt size assigned to it being updated to reflect the new size. This is a duration (e.g., `10m` for 10 minutes).

## HostedClusterSizing Controller

The `HostedClusterSizing` controller determines the number of nodes associated with a `HostedCluster` either from the `HostedControlPlane.Status` or by iterating through the nodepools and counting the nodepools associated with the `HostedCluster`. It then compares the number of nodes against the minimum and maximum sizes set for each tshirt size in the `ClusterSizingConfiguration`. Based on this comparison, it applies a label to the `HostedCluster` with the appropriate tshirt size. Depending on the settings in the `ClusterSizingConfiguration`, it can wait a specified amount of time before transitioning between tshirt sizes using a sliding window, ensuring that only a limited number of transitions (e.g., 5 transitions) can occur within a specified time frame (e.g., 20 minutes).

The controller also updates the status of the `HostedCluster`, reporting the computed cluster size, indicating if a tshirt size transition is pending, and specifying if the cluster requires a transition to a different size.

## Azure Scheduler Controller

The Azure scheduler controller is straightforward. It checks the label set by the `HostedClusterSizing` controller and retrieves the cluster sizing configuration associated with the tshirt size. Based on the configuration, it can modify the `HostedCluster` with annotations for the specified fields. These annotations are then used by different controllers to propagate the required changes to the appropriate pods and containers.

## How to Use

### Prerequisites

- AKS cluster with cluster-autoscaler enabled and using Standard\_D4s\_v4 VMs for this example. (--enable-cluster-autoscaler flag when installing AKS cluster, with --min-count 2 --max-count 6)
- Hypershift operator with size tagging enabled. (--enable-size-tagging flag when installing hypershift operator)
- ClusterSizingConfiguration resource created. (A default clusterSizingConfiguration resource is created by the hypershift operator)
- A HostedCluster in the Completed state.
- A Nodepool with 2 nodes associated with the HostedCluster.


### Steps

In the example below we will use a HostedCluster with the name 'pstefans-3' in the 'clusters' namespace and the nodepool 'pstefans-3' in the 'clusters' namespace.

1. The AKS cluster should have only 2 nodes at this point.

    ```shell
    oc get nodes
    NAME                                STATUS   ROLES    AGE     VERSION
    aks-nodepool1-11371333-vmss000000   Ready    <none>   3h43m   v1.31.1
    aks-nodepool1-11371333-vmss000002   Ready    <none>   3h43m   v1.31.1
    ```

2. Edit the `ClusterSizingConfiguration` resource with the following spec:

    ```shell
    oc edit clustersizingconfiguration cluster
    ```

    ```yaml
    spec:
      concurrency:
        limit: 5
        slidingWindow: 0s
      sizes:
      - criteria:
          from: 0
          to: 2
        name: small
      - criteria:
          from: 3
          to: 4
        effects:
          resourceRequests:
          - containerName: kube-apiserver
            cpu: 3
            deploymentName: kube-apiserver
          - containerName: control-plane-operator
            cpu: 3
            deploymentName: control-plane-operator
        name: medium
      - criteria:
          from: 5
        name: large
      transitionDelay:
        decrease: 0s
        increase: 0s
    ```

3. Scale nodepool up to 3 nodes:

    ```shell
    oc scale nodepool pstefans-3 \
      --namespace clusters \
      --replicas 3
    ```

4. Once node pool scales successfully, the `HostedCluster` will be updated with the new tshirt size label and should have the resource request overrides annotations applied to the HC and the relevant controllers should pick this up and set it on the specified pods.

    ```shell
    oc get deployment kube-apiserver -n clusters-pstefans-3 -o json | jq '.spec.template.spec.containers[] | select(.name == "kube-apiserver") | .resources'
    ```

    ```json
    {
      "requests": {
        "cpu": "3",
        "memory": "2Gi"
      }
    }
    ```

    ```shell
    oc get deployment control-plane-operator -n clusters-pstefans-3 -o json | jq '.spec.template.spec.containers[] | select(.name == "control-plane-operator") | .resources'
    ```

    ```json
    {
      "requests": {
        "cpu": "3",
        "memory": "80Mi"
      }
    }
    ```

    ```shell
    oc get hc pstefans-3 -n clusters  -o yaml | grep resource-request-override.hypershift.openshift.io
    resource-request-override.hypershift.openshift.io/control-plane-operator.control-plane-operator: cpu=3
    resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver: cpu=3
    ```

5. You should now see the autoscaler scaled the nodes on the AKS cluster to 3 as we requested 3 CPU cores for the kube-apiserver and control-plane-operator on a nodepool with max 4 cores. So each deployment will nearly request nearly a full node to itself.

    ```shell
    oc get nodes
    NAME                                STATUS   ROLES    AGE     VERSION
    aks-nodepool1-11371333-vmss000000   Ready    <none>   4h8m    v1.31.1
    aks-nodepool1-11371333-vmss000002   Ready    <none>   4h8m    v1.31.1
    aks-nodepool1-11371333-vmss000003   Ready    <none>   9m31s   v1.31.1
    ```

6. You should now see that each of the deployments we changed the resource requests for are running on a different node with sufficient compute.

    ```shell
    kubectl get pods --all-namespaces --field-selector spec.nodeName=aks-nodepool1-11371333-vmss000003
    ```

    ```shell
    NAMESPACE             NAME                                     READY   STATUS    RESTARTS   AGE
    clusters-pstefans-3   kube-apiserver-549c75cb99-jj964          4/4     Running   0          12m
    ```

    ```shell
    kubectl get pods --all-namespaces --field-selector spec.nodeName=aks-nodepool1-11371333-vmss000002
    ```

    ```shell
    NAMESPACE             NAME                                      READY   STATUS    RESTARTS   AGE
    clusters-pstefans-3   control-plane-operator-69b894d9dd-cxv2z   1/1     Running   0          14m
    ```