# Configure Metrics Sets

HyperShift creates ServiceMonitor resources in each control plane namespace that allow
a Prometheus stack to scrape metrics from the control planes. ServiceMonitors use metrics relabelings
to define which metrics are included or excluded from a particular component (etcd, Kube API server, etc)
The number of metrics produced by control planes has a direct impact on resource requirements of
the monitoring stack scraping them.

Instead of producing a fixed number of metrics that apply to all situations, HyperShift allows
configuration of a "metrics set" that identifies a set of metrics to produce per control plane.

The following metrics sets are supported:

* `Telemetry` - metrics needed for telemetry. This is the default and the smallest
   set of metrics.
* `SRE` - Configurable metrics set, intended to include necessary metrics to produce alerts and
   allow troubleshooting of control plane components.
* `All` - all the metrics produced by standalone OCP control plane components.

The metrics set is configured by setting the `METRICS_SET` environment variable in the HyperShift
operator deployment:

```
oc set env -n hypershift deployment/operator METRICS_SET=All
```

## Configuring the SRE Metrics Set

When the SRE metrics set is specified, the HyperShift operator looks for a ConfigMap named
`sre-metric-set` with a single key: `config`. The value of the `config` key should contain a set
of RelabelConfigs organized by control plane component. An example of this configuration can be
found in `support/metrics/testdata/sreconfig.yaml` in this repository.

The following components can be specified:

* etcd
* kubeAPIServer
* kubeControllerManager
* openshiftAPIServer
* openshiftControllerManager
* openshiftRouteControllerManager
* cvo
* olm
* catalogOperator
* registryOperator
* nodeTuningOperator
* controlPlaneOperator
* hostedClusterConfigOperator