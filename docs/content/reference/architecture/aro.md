# Hosted Control Planes in Azure Red Hat OpenShift

## Shared Ingress Network Diagram

![Shared Ingress in ARO/HCP](/images/diagram-arohcp-shared-ingress.png)

The diagram above shows how HyperShift adds a new HAProxy in the data plane that uses proxy protocol 2.0 TLV headers to be able to direct the requests that the dataplane does for the Kubernetes API to the remote single shared ingress load balancer.

The common Shared ingress load balancer then directs the requests using the aforementioned proxy protocol 2.0 TLVs to the appropriate Kubernetes API servers.
