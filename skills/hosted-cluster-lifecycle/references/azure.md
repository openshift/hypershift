# Self-managed Azure overlay

This guide covers self-managed HyperShift on Azure. ARO HCP has a different
managed provisioning path; do not apply these setup commands to that service.
The shared readiness model is code-derived. The retained live observations
were AWS; no Azure duration baseline was measured.

For setup, resolve subscription, tenant, location, resource-group ownership,
VNet/subnets, endpoint access and the selected workload-identity workflow.
Use existing authorized infrastructure/identity outputs when supplied.

```bash
set -euo pipefail
umask 077
az account show --query '{subscription:id,tenant:tenantId,name:name}' --output json \
  > "$RESTRICTED_DIR/azure-account.raw.json" \
  2> "$RESTRICTED_DIR/azure-account.raw.stderr"
mgmt_oc -n "$HCP_NAMESPACE" \
  get azureclusters.infrastructure.cluster.x-k8s.io,azureclusteridentities.infrastructure.cluster.x-k8s.io,azuremachines.infrastructure.cluster.x-k8s.io \
  -o json > "$RESTRICTED_DIR/azure-provider-resources.raw.json" \
  2> "$RESTRICTED_DIR/azure-provider-resources.raw.stderr"
jq '[.items[] | {kind, phase: .status.phase,
  conditions: [.status.conditions[]? |
    {type, status, lastTransitionTime, observedGeneration}]}]' \
  "$RESTRICTED_DIR/azure-provider-resources.raw.json" \
  2> "$RESTRICTED_DIR/azure-provider-projection.raw.stderr"
```

Check API discovery/version before treating an absent resource kind as a
missing object. CAPZ runs in the common `capi-provider` deployment. Follow its
exact condition/error and credential/identity references. Inspect managed
identity and federated-credential issuer, subject, audience, resource scope
and role assignment against the actual cluster configuration. Do not propose
broad role assignments or switch subscriptions to bypass a failed query.

For endpoint issues, inspect HCP Services and DNS. Private clusters also need
the relevant `AzurePrivateLinkService` conditions, private endpoint and private
DNS path. Discover the resource API rather than assuming every installation
uses this private-connectivity mode. Use the advertised endpoint/Service port;
the code examined for the roadmap uses Azure KAS LB port 7443 to avoid a
management LB rule collision, so do not hardcode 6443 in a connectivity test.

For a concrete AzureMachine/provider ID, inspect the corresponding VM power/
provisioning state, boot diagnostics, image compatibility, NIC/subnet/NSG and
ignition/guest API DNS reachability. Keep subscription and resource-group
queries scoped to that VM. Booting, fetching ignition and registering a guest
Node are separate gates.

For local source verification when a checkout is available:
`control-plane-operator/controllers/hostedcontrolplane/infra/infra.go`,
the hostedcluster Azure platform reconciliation, and the matching CAPZ version.
Provider installation flags, supported identity options and role requirements
must come from the selected CLI/docs/source revision, not the AWS observations.
