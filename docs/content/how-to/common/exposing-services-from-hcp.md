# Exposing the Hosted Control Plane Services

To publish the services from the Hosted Control Plane, we need to understand the available strategies and their implications. Let's explore them.

## Service Publishing Strategies

Let's delve into the motivations for each of the strategies.

The **NodePort** strategy allows you to expose services without requiring a logical LoadBalancer like MetalLB or similar infrastructure. It is one of the simplest methods to implement. This strategy is supported by all services; however, the limitation arises in high availability (HA) environments where you will be pointing to one of the NodePorts instead of all three.

The **LoadBalancer** strategy enables you to expose certain services through a load balancer. While not all services support this strategy, it is the preferred method for exposing the KubeApiServer, as it allows for a single entry point in an HA configuration without relying in the Ingress Controller of the Manamgement cluster.

The **Route** strategy allows you to expose the HostedControlPlane services using the ingress of the Management OpenShift cluster. This strategy is supported by all services but kubeapi-server.

### NodePort

Exposing a service via NodePort is a method used in OpenShift to make a service accessible from outside the cluster. When you expose a service using NodePort, OpenShift allocates a port on each node in the cluster (if the cluster availability policy is set to HighlyAvailable). This port on each node is mapped to the port of the service, allowing external traffic to reach the service by accessing any node's IP address and the allocated NodePort.

This is the default configuration when you use `Agent` and `None` provider platforms. The services relevant for on-premise platforms are:

- APIServer
- OAuthServer
- Konnectivity
- Ignition

!!! Note
    If any of the services are not relevant for your deployment, it is not necessary to specify them.

Here is how it looks in the HostedCluster CR:

```yaml
spec:
...
...
  services:
  - service: APIServer
    servicePublishingStrategy:
      nodePort:
        address: <IP_ADDRESS>
        port: <PORT>
      type: NodePort
  - service: OAuthServer
    servicePublishingStrategy:
      nodePort:
        address: 10.103.101.101
      type: NodePort
  - service: Konnectivity
    servicePublishingStrategy:
      nodePort:
        address: 10.103.101.101
      type: NodePort
  - service: Ignition
    servicePublishingStrategy:
      nodePort:
        address: 10.103.101.101
      type: NodePort
...
...
```

### Route

OpenShift routes provide a way to expose services within the cluster to external clients. A route in OpenShift maps an external request (typically HTTP/HTTPS) to an internal service. The route specifies a hostname that external clients will use to access the service. OpenShift’s router (based on HAProxy) will handle the traffic coming to this hostname.

HostedControlPlanes operate in two domains: the Control Plane and the Data Plane. The Control Plane uses routes through the MGMT Cluster ingress to expose services for each of the HostedControlPlanes, and the routes are created in the HostedControlPlane namespace. For the Data Plane, the Ingress handles `*.apps.subdomain.tld` URLs, and all routes under this wildcard are directed to the Namespace by the OpenShift Router on the worker nodes.

#### Route Hostname Assignment

Routes require a hostname (`spec.host` field) to be routable. HyperShift supports two patterns for assigning route hostnames:

**Pattern A: Explicit Hostnames** (Recommended for production)

When creating a HostedCluster with the `--external-dns-domain` flag, explicit hostnames are assigned to each route:

```yaml
spec:
  services:
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
      route:
        hostname: oauth-mycluster.service-provider-domain.com
  - service: Konnectivity
    servicePublishingStrategy:
      type: Route
      route:
        hostname: konnectivity-mycluster.service-provider-domain.com
```

This pattern:
- Used by cloud platforms (AWS, Azure) with external-DNS integration
- Provides stable, predictable DNS names
- See [External-DNS Guide](../aws/external-dns.md) for setup details

**Pattern B: Auto-Generated Hostnames**

When routes don't have explicit hostnames, they depend on the `DEFAULT_INGRESS_DOMAIN` environment variable in the control-plane-operator:

```yaml
spec:
  services:
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
      # No route.hostname field
```

If `DEFAULT_INGRESS_DOMAIN` is set, hostnames are auto-generated like: `oauth-<namespace>.${DEFAULT_INGRESS_DOMAIN}`

!!! warning "Platform=None Considerations"
    Platform=None clusters using the Route strategy must ensure routes get valid hostnames through one of these methods:

    1. Set explicit hostnames in the HostedCluster spec (Pattern A)
    2. Configure `DEFAULT_INGRESS_DOMAIN` in the control-plane-operator deployment

    **If neither is configured**, routes will be created with **empty `spec.host` fields**, which:

    - Cannot be routed by shared ingress (HAProxy requires hostnames for SNI routing)
    - Causes `InfrastructureReady=False` status
    - Prevents kubeconfig publication
    - Blocks cluster from becoming functional

    For architecture details, see [Shared Ingress Architecture](../../reference/architecture/managed-azure/shared-ingress.md).

The usual configuration for the Hosted Cluster is similar to the LoadBalancer setup we will discuss next.

### LoadBalancer

The LoadBalancer strategy in OpenShift is used to expose services to external clients using an external load balancer. When you create a service of type LoadBalancer, Kubernetes interacts with the underlying cloud platform or appropriate LoadBalancer controllers to provision an external load balancer, which then routes traffic to the service's endpoints (pods).

This is how looks like the most common configuration to expose the services from HCP side:

```yaml
spec:
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
  - service: OIDC
    servicePublishingStrategy:
      type: Route
      Route:
        hostname: <URL>
  - service: Konnectivity
    servicePublishingStrate
      type: Route
  - service: Ignition
    servicePublishingStrategy:
      type: Route
```

If you wanna know more about how to expose the ingress service in the Data Plane side, please access [the Recipes section](../../recipes/index.md) to see how to do it with MetalLB.
