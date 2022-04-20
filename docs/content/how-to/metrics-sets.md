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
* `SRE` - metrics in `Telemetry` plus those needed for service reliability monitoring of HyperShift control planes.
   Includes metrics necessary to produce alerts and allow troubleshooting of control plane
   components.
* `All` - all the metrics produced by standalone OCP control plane components.

The metrics set is configured by setting the `METRICS_SET` environment variable in the HyperShift
operator deployment:

```
oc set env -n hypershift deployment/operator METRICS_SET=All
```
