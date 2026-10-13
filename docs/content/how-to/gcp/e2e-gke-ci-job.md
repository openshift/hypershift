# GCP E2E CI Job (e2e-gke)

## What is it

The `e2e-gke` job is a presubmit CI job in the `openshift/hypershift` repo that validates GCP platform changes end-to-end. It creates two ephemeral GCP projects (control plane + hosted cluster), provisions a GKE Autopilot cluster, installs the HyperShift operator, creates a hosted cluster with WIF and PSC, validates it (TestCreateCluster), and tears everything down.

## When does it trigger

The job triggers on PRs to `openshift/hypershift` when files matching GCP-related code paths are modified:

```
api/hypershift/v1beta1/gcp.*
hypershift-operator/controllers/.*/gcp.*
control-plane-operator/controllers/.*/gcp.*
cmd/cluster/gcp/.*
cmd/nodepool/gcp/.*
```

It can also be triggered manually with `/test e2e-gke`.

Current flags: `always_run: false`, `optional: true`, `skip_report: true` — meaning it won't block PRs and results aren't posted as GitHub status checks yet.

## What happens if it fails

- The job result is not reported on the PR (`skip_report: true`), so failures don't block merging
- Post steps always run, including deprovision — GCP projects are cleaned up even if the test or the job is aborted
- Artifacts (logs, junit, hypershift-dump) are uploaded to GCS for debugging
- Concurrency is limited to 10 parallel runs via Boskos leases

## CI Workflow

The job uses the `hypershift-gcp-gke-e2e` workflow defined in [openshift/release](https://github.com/openshift/release/tree/master/ci-operator/step-registry/hypershift/gcp):

**Pre phase:**

1. `ipi-install-rbac` — Grant image-puller permissions
2. `hypershift-gcp-gke-provision` — Create GCP projects, VPC, PSC subnet, GKE Autopilot cluster
3. `hypershift-gcp-gke-prerequisites` — Install CRDs and cert-manager
4. `hypershift-install` — Install HyperShift operator with GCP support
5. `hypershift-gcp-control-plane-setup` — Configure operator WIF for PSC and ExternalDNS
6. `hypershift-gcp-hosted-cluster-setup` — Create RSA keypair, WIF pool/SAs, HC network

**Test phase:**

7. `hypershift-gcp-run-e2e` — Run TestCreateCluster

**Post phase:**

8. `hypershift-dump` — Collect logs and artifacts
9. `hypershift-gcp-gke-deprovision` — Delete GCP projects, GKE cluster, DNS records
