# Create Monitoring Dashboard per Hosted Cluster

The HyperShift operator can create/destroy a separate monitoring dashboard in the console of the management cluster for each HostedCluster that it manages. This functionality can be optionally enabled on installation of the HyperShift operator.

### Enable Monitoring Dashboards

To enable monitoring dashboards, use the `--monitoring-dashboards` flag when running `hypershift install`. Alternatively, to enable monitoring dashboards in an existing installation, set the `MONITORING_DASHBOARDS` environment variable to `1` on the hypershift operator deployment:

```
oc set env deployment/operator -n hypershift MONITORING_DASHBOARDS=1
```

### Dashboards

When monitoring dashboards are enabled, the HyperShift operator creates a configmap named `cp-[NAMESPACE]-[NAME]` in the `openshift-config-managed` namespace (where NAMESPACE is the namespace of the HostedCluster and NAME is the name of the HostedCluster) for each HostedCluster that the operator manages. This results in a new dashboard getting added under Observe -> Dashboards in the administrative console of the management cluster. When a HostedCluster is deleted, its corresponding dashboard is also deleted.

### Customize Monitoring Dashboards

To generate per-cluster dashboards, the HyperShift operator uses a template stored in a ConfigMap named `monitoring-dashboard-template` in the operator namespace (`hypershift`). This template contains a set of grafana panels that contain the various metrics that should go on the dashboard. Edit the content of this ConfigMap to customize the dashboards. When a particular HostedCluster's dashboard is generated, the following strings will be replaced with values that correspond to the specific HostedCluster:

| Name                          | Description                                                                      |
|-------------------------------|----------------------------------------------------------------------------------|
| `__NAME__`                    | The name of the HostedCluster                                                    |
| `__NAMESPACE__`               | The namespace of the HostedCluster                                               |
| `__CONTROL_PLANE_NAMESPACE__` | The namespace where the control plane pods of the HostedCluster are placed       |
| `__CLUSTER_ID__`              | The UUID of the HostedCluster (matches the `_id` label of HostedCluster metrics) |