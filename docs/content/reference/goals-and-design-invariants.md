# Project goals

These are desired project goals which drive the design invariants stated below. Goals and scope may vary as the project evolves.

- Provide an API to express intent to create OpenShift Container Platform (OCP) clusters with a hosted control plane topology on existing infrastructure.
- Decouple control and data plane.
  - Enable segregation of ownership and responsibility for different [personas](https://hypershift.pages.dev/reference/concepts-and-personas/).
  - Security.
  - Cost efficiency.

## Design invariants

- Communication between management cluster and a hosted cluster is unidirectional. A hosted cluster has no awareness of a management cluster.
- Communication between management cluster and a hosted cluster is only allowed from within each particular control plane namespace.
- Compute worker Nodes should not run anything beyond user workloads.
  - A hosted cluster should not expose CRDs, CRs or Pods that enable users to manipulate HyperShift owned features.
- HyperShift components should not own or manage user infrastructure platform credentials.

### CP and Data Plane Ingress

- Control plane (CP) ingress and guest cluster (data plane) ingress are orthogonal. They are handled by separate components, have separate implementations, and should not be conflated.

- CP ingress is handled by HyperShift. A dedicated (or shared) router (HAProxy pod) is deployed in the management cluster. It is exposed to the guest cluster's private network via a cloud-specific Private Link (AWS PrivateLink, Azure Private Link Service, Swift...). The private-router uses SNI-based routing to forward traffic to the appropriate CP service (KAS, OAuth, Konnectivity, Ignition).

- Data plane (guest cluster) ingress — i.e. application traffic under `*.apps` — is handled by the ingress operator running inside the guest cluster. This is standard OpenShift ingress, not something HyperShift's CP infrastructure manages. The CP private-router lives on the management side and does not know how to resolve guest cluster workloads.

- A private topology dictates how CP ingress endpoints are exposed (e.g. only via Private Link, not public LB). It may also influence the desired visibility of guest cluster ingress, but the two are not inherently linked.

- DNS for private clusters uses a synthetic `<cluster-name>.hypershift.local` zone. This is an internal, non-configurable domain automatically managed by HyperShift. Records in this zone include:
  - `api.<cluster-name>.hypershift.local` → private endpoint IP
  - `*.apps.<cluster-name>.hypershift.local` → private endpoint IP

  These `*.apps` records in the `.hypershift.local` zone exist for CP-resident services that are exposed as routes (OAuth, Ignition, Konnectivity), not for guest cluster application traffic.

- The `.hypershift.local` `*.apps` wildcard is distinct from `*.apps.<cluster>.<basedomain>`. The former resolves CP service routes via the private endpoint. The latter is the guest cluster's application ingress domain, managed by the ingress operator on the data plane — not by HyperShift's CP Private Link infrastructure.

- On AWS, the reason both `api` and `*.apps` records exist in the `.hypershift.local` zone is historical: originally there was support for KAS having its own LB, which would have required two separate private endpoints and therefore two distinct domain resolutions. A similar pattern may be needed in the future for Azure if OAuth gets its own LB, but that is a separate concern from guest cluster traffic routing.

- PRs modifying private DNS should validate traffic flow, not just DNS records. E2e tests should demonstrate that a traffic journey previously blocked is now enabled by the change, rather than simply asserting that DNS records exist in infrastructure.