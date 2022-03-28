# Use Service-level DNS for Control Plane Services

There are four service that are exposed by a Hosted Control Plane (HCP)

* `APIServer`
* `OAuthService`
* `Konnectivity`
* `Ignition`

Each of these services is exposed using a `servicePublishingStrategy` in the HostedCluster spec.

By default, for `servicePublishingStrategy` types `LoadBalancer` and `Route`, the service will be published using the hostname of the LoadBalancer found in the status of the `Service` with type `LoadBalancer`, or in the `status.host` field of the `Route`.

This is acceptable for Hypershift development environments.  However, when deploying Hypershift in a managed service context, this method leaks the ingress subdomain of the underlying management cluster and can limit options for management cluster lifecycling and disaster recovery.  For example, if the AWS load balancer for a service is lost for whatever reason, the DNS name of that load balancer is in the kubelet kubeconfig of each node in the guest cluster.  Restoring the cluster would involve an out-of-band update of all kubelet kubeconfigs on existing nodes.

Having a DNS indirection layer on top of the `LoadBalancer` and `Route` publishing types allows a managed service operator to publish all _public_ HostedCluster `services` using a service-level domain.  This allows remampping on the DNS name to a new `LoadBalancer` or `Route` and does not expose the ingress domain of the management cluster.

## external-dns

Hypershift uses [external-dns](https://github.com/openshift/external-dns) to achieve this indirection.

`external-dns` is optionally deployed alongside the `hypershift-operator` in the `hypershift` namespace of the management cluster. It watches the cluster for `Services` or `Routes` with the `external-dns.alpha.kubernetes.io/hostname` annotation.  This value of this annotation is used to create a DNS record pointing to the `Service` (A record) or `Route` (CNAME record).

`hypershift install` will create the `external-dns` deployment if the proper flags are set:

```
hypershift install --external-dns-provider=aws --external-dns-credentials=route53-aws-creds --external-dns-domain-filter=service.hypershift.example.org ...
```

where `external-dns-provider` is the DNS provider that manages the service-level DNS zone, `external-dns-credentials` is the credentials file appropriate for the specified provider, and `external-dns-domain-filter` is the service-level domain.

## HostedCluster with Service Hostnames

Create a HostedCluster that sets `hostname` for `LoadBalancer` and `Route` services:

```
hypershift create cluster aws --name=example --endpoint-access=PublicAndPrivate --external-dns-domain=service.hypershift.example.org ...
```

The resulting HostedCluster `services` block looks like this:

```
  platform:
    aws:
      endpointAccess: PublicAndPrivate
...
  services:
  - service: APIServer
    servicePublishingStrategy:
      loadBalancer:
        hostname: api-example.service.hypershift.example.org
      type: LoadBalancer
  - service: OAuthServer
    servicePublishingStrategy:
      route:
        hostname: oauth-example.service.hypershift.example.org
      type: Route
  - service: Konnectivity
    servicePublishingStrategy:
      type: Route
  - service: Ignition
    servicePublishingStrategy:
      type: Route
```

When the `Services` and `Routes` are created by the Control Plane Operator (CPO), it will annotate them with the `external-dns.alpha.kubernetes.io/hostname` annotation. The value will be the `hostname` field in the `servicePublishingStrategy` for that type.  The CPO uses this name blindly for the service endpoints and assumes that if `hostname` is set, there is some mechanism external-dns or otherwise, that will create the DNS records.

There is an interaction between the `spec.platform.aws.endpointAccess` and which services are permitted to set `hostname` when using [AWS Private clustering](aws/deploy-aws-private-clusters.md).  Only *public* services can have service-level DNS indirection.  Private services use the `hypershift.local` private zone and it is not valid to set `hostname` for `services` that are private for a given `endpointAccess` type.

The following table notes when it is valid to set hostname for a particular `service` and `endpointAccess` combination:

|              | Public | PublicAndPrivate | Private |
|--------------|--------|------------------|---------|
| APIServer    | Y      | Y                | N       |
| OAuthServer  | Y      | Y                | N       |
| Konnectivity | Y      | N                | N       |
| Ingition     | Y      | N                | N       |
