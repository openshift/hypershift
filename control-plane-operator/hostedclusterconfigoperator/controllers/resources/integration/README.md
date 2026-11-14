# Control-plane integration projections

The integration owner reconciles hosted-cluster resources that connect hosted-cluster
consumers to control-plane processes:

- OpenShift API server APIServices (trusting the control-plane root CA), cluster Service and Endpoints.
- OAuth API server APIServices, cluster Service and Endpoints, and the kubeadmin password hash.
- The OAuth serving-cert CA bundle, OAuth clients and OAuth serving-cert RBAC.

Manifest constructors and mutators remain in `manifests`, `pkg/manifests/hcco`, `oapi`,
`oauth` and `kubeadminpassword`. Only projection selection, ordering and error policy live here.

`Reconcile` is the single entry point. Its `Stage` keeps the two existing root phase positions:

- `APIServiceProjections` in API services/OAuth, after the connection conditions and before
  the kube-apiserver ServiceMonitor.
- `OAuthProjections` in networking/secrets, after the proxy CA bundle and before cloud credentials.

`Params` carries only the HCP namespace, OAuth enablement and the external OAuth endpoint; it
never retains the HCP. OAuth projections are selected only when OAuth is enabled, so external
OIDC clusters get neither OAuth objects nor control-plane OAuth reads, and nothing is cleaned up.

`HostedCluster` and `ControlPlane` are distinct adapters over the root's existing clients and
upsert. The control-plane adapter only reads and upserts: its only write is the kubeadmin hash
annotation on the control-plane `kubeadmin-password` secret. Control-plane inputs are the root
CA, the OpenShift and OAuth API server Services, `kubeadmin-password` and the OAuth master CA.

A missing control-plane input or a Service without a ClusterIP is reported and the remaining
projections continue; existing hosted endpoints are left untouched until the ClusterIP exists.
Readiness recovers through the root's requeue, because these control-plane inputs are not watched.

`Reconcile` returns one aggregate whose entries are the root-visible wrapped errors, in order.
The root flattens it, so each failure stays a separate root aggregate entry, and keeps shared
input loading, logging, HCP reconciliation status and watch registration.

Not owned here: Konnectivity, connection checks and conditions, the kube-control-plane signer
and kubelet serving CA, `kubernetes.default` endpoints, external OIDC, monitoring and OLM.

Direct projection tests live here; root phase/error/status regression tests remain in resources.
