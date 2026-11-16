# Karpenter Operator image selection

The embedded Karpenter Operator ships in the HyperShift Operator image and
follows HO's release cadence, independently of the hosted cluster's OCP version.
For standalone KO, the pin embedded in HO preserves that release pairing without
requiring a matching OCP payload or an override on every HostedCluster.

Image selection applies only when `--enable-standalone-karpenter-operator` and
the HO `KarpenterOperator` TechPreview feature gate enable standalone KO. Image
overrides do not enable standalone mode or change the embedded operator path.
Disabled AutoNode cleanup does not resolve the image.

## Consumer configuration

### Managed services

With standalone KO enabled, leave `KARPENTER_OPERATOR_IMAGE_OVERRIDE` and the
HostedCluster image annotation unset to consume the pin included in HO. No
additional image configuration is needed. If a different KO build is required,
set `KARPENTER_OPERATOR_IMAGE_OVERRIDE` on the HO Deployment to its pullspec.

The environment override applies to all standalone KO deployments managed by
that HO. Updating the environment on the HO Deployment rolls HO and applies the new configuration
during reconciliation. Updating a registry tag alone does not trigger a rollout.

### Development CI

Set `KARPENTER_OPERATOR_IMAGE_OVERRIDE` to the image that the job intends to
test. For HyperShift development, this is the promoted KO main build. For KO
development, this is the KO PR build tested against the promoted HO main build.
CI resolves the image dependency and supplies its pullspec; HO does not resolve
Git branches itself.

The override accepts tags or digests. CI should supply the digest resolved for
that job. For example, a tag-based override is:

```yaml
KARPENTER_OPERATOR_IMAGE_OVERRIDE: registry.ci.openshift.org/ocp/5.1:karpenter-operator
```

CI must reject an empty image dependency and verify that the KO Deployment uses
the intended image. Otherwise, a missing override could silently select the pin
or payload instead.

### Pinned compatibility and HO release validation

Leave the environment override unset and avoid HostedCluster image annotations.
These tests must exercise the KO build paired with the HO build under test,
not an injected development image.

Check that the candidate HO build contains a nonempty pin and verify that the
KO Deployment uses that pullspec. If the pin is absent or empty, tests exercise
the payload fallback rather than a pinned HO/KO pair.

### Per-cluster development and debugging

Set the `hypershift.openshift.io/karpenter-operator-image` HostedCluster annotation
when only one cluster needs a different image. It takes precedence over the
HO-wide environment override and pin.

## Image precedence and fallback

Standalone KO deployments use the first configured image:

1. The nonempty HostedCluster image annotation.
2. The nonempty `KARPENTER_OPERATOR_IMAGE_OVERRIDE` environment variable on HO.
3. The image pinned in `overrides.yaml`.
4. The `karpenter-operator` image from the hosted cluster's release payload.

An unset or empty environment variable means no environment override. An absent
pin file or empty pinned image falls back to the payload. Payload consumption
is a compatibility fallback, not the intended HCP release mechanism or CI
image-selection method.

A nonempty environment override must be a valid pullspec. A pin file must be
valid YAML when read, and a nonempty pinned image must include a digest. Invalid
configuration returns an error rather than falling through to a lower-priority
image. Higher-priority overrides bypass validation of unused lower-priority
images. Image pull failures do not trigger fallback.

## Updating the pin

Set `image` in `overrides.yaml` to the full pullspec of an ART-promoted HCP build,
including its digest. The file is embedded in HO at build time. Commit the bump
and run the AutoNode presubmits before merging. Keep this README when removing
`overrides.yaml` so the embedded directory remains present.

A digest bump rolls the KO Deployments consuming the pin when the new HO image
is deployed, but only when a new HO image is deployed that contains a new pin.
