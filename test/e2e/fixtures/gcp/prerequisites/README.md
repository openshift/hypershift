# GKE CI prerequisites

These unmodified upstream manifests are bundled in the `hypershift-tests` image
at `/hypershift/test/e2e/fixtures/gcp/prerequisites`. The
`hypershift-gcp-gke-prerequisites` step in `openshift/release` installs them
without downloading manifests from GitHub at runtime.

## Pinned sources

| Component | Revision | Files |
| --- | --- | --- |
| Prometheus Operator | `854d33a40caad1dd773f73deb55726732803bcb8` | ServiceMonitor, PrometheusRule, PodMonitor CRDs |
| OpenShift API | `6bababe9164ea6c78274fd79c94a3f951f8d5ab2` | Route CRD |
| cert-manager | `v1.14.0` release, pinned by SHA-256 | Complete installation manifest |

The Prometheus revision is the latest upstream commit before the successful
CRD downloads in the November 8, 2026 CI run. Previously these three files
tracked mutable `main`. Route and cert-manager retain their previous pins.
`sources.json` records exact download URLs, revisions, and SHA-256 checksums
for all upstream files, including their Apache-2.0 license texts.

## Updating

1. Choose an immutable upstream commit for all three Prometheus CRDs together.
   Keep Route and cert-manager pinned to explicit revisions/releases as well.
2. Download the files from their recorded upstream paths, retaining license
   notices. Do not edit generated upstream YAML by hand.
3. Update `sources.json` with the new URLs, revisions, and SHA-256 checksums;
   update this table. Refresh the corresponding license if necessary.
4. Update version assertions in `../verify_prerequisites.py` and the
   prerequisite script in `openshift/release` when changing cert-manager.
   Review its deployment/container names and GKE Autopilot adjustments.
5. Run `make verify-gcp-prerequisites`, build the e2e image, and test installation
   on a fresh GKE cluster. Verification itself never downloads upstream files.

The CI script copies cert-manager YAML to a temporary file before changing
the leader-election namespace and patches deployment resource requests after
installation. Those adjustments are deliberately not baked into these files.

Publish an image containing this directory before switching a release step
to consume it. Older images lack the bundle; installation must fail clearly
rather than silently falling back to a GitHub download.
