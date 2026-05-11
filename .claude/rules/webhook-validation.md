---
paths:
  - "hypershift-operator/controllers/hostedcluster/hostedcluster_webhook*.go"
---

# Do Not Extend the Webhook

The webhook in `hostedcluster_webhook.go` is **not a general-purpose extension point**. Do not add new validation or defaulting logic here.

HyperShift uses CRD CEL validation rules instead of webhooks. The webhook exists only for KubeVirt platform-specific needs (defaulting and JSON patch annotation validation).

## Why

Webhooks add latency and operational complexity that CEL avoids:

- **Konnectivity overhead.** In hosted-on-hosted topologies (e.g., IBM Cloud, where management clusters are themselves hosted clusters), a webhook request travels: apiserver → konnectivity server → konnectivity agent → node → webhook service (possibly on a different node). This adds significant per-request latency to every API write.
- **Availability coupling.** A down or slow webhook blocks all writes to the resource. CEL runs inside the API server with no external dependency.
- **Operational overhead.** Webhooks require TLS certificate management, webhook configurations, and a running service.

## What to Do Instead

Add new validation as CEL markers on the API types in `api/hypershift/v1beta1/` and cover them with envtests. See `api/AGENTS.md` and `test/envtest/README.md`.

The exception is CAPI resources, where a conversion webhook is required during the v1beta2 migration period.
