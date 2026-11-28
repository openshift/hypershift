# Karpenter Operator image selection

The embedded Karpenter Operator ships in the HyperShift Operator image and
follows HO's release cadence, independently of the hosted cluster's OCP version.

Standalone KO is developed in `openshift/karpenter-operator`. Its HCP stream
follows `main` and is released at the Autoscale team's own cadence.

Normal delivery of KO updates to fleets remains tied to HO releases. When a KO
change is needed, the Autoscale team updates the pin in HO source to a tested,
ART-promoted HCP build. Consumers then update HO to a release containing that
pin. Each HO release selects the KO version it was tested with, independently
of the hosted cluster's payload. See the
[enhancement's HCP release model](https://github.com/openshift/enhancements/pull/2007)
for the release-stream rationale.

Image selection applies only when `--enable-standalone-karpenter-operator` and
the HO `KarpenterOperator` feature gate enable standalone KO, which is enabled in the default feature set.
Image overrides do not enable standalone mode or change the embedded operator path.
Disabled AutoNode cleanup does not resolve the image.

## Consumer configuration

### Managed services

With standalone KO enabled, leave `KARPENTER_OPERATOR_IMAGE_OVERRIDE` unset to
consume the pin included in HO. No additional image configuration is needed.
Normal fleet updates should use the KO version paired with the HO release.

For an urgent KO hotfix that cannot wait for a new HO release, set
`KARPENTER_OPERATOR_IMAGE_OVERRIDE` on the HO Deployment to the hotfix image's
pullspec. This bypasses the normal pinned HO/KO pairing without requiring a new
HO image. Verify that the hotfix build works with the deployed HO version.

The environment override is HO-wide rather than per-cluster. Updating it on the
HO Deployment rolls HO and applies the new configuration during reconciliation.
Updating a registry tag alone does not trigger a rollout.

Remove the environment override after adopting an HO release whose pin includes
the fix. While the override remains set, it takes precedence over pins delivered
in later HO releases.

### Development CI

Set `KARPENTER_OPERATOR_IMAGE_OVERRIDE` to the image that the job intends to
test. For HyperShift development, this is the promoted KO main build. For KO
development, this is the KO PR build tested against the promoted HO main build.
CI resolves the image dependency and supplies its pullspec; HO does not resolve
Git branches itself.

The override must include an explicit registry and may use a tag or digest.
CI should supply the digest resolved for that job, not a moving tag. Replace
`<digest>` below with that resolved digest:

```yaml
KARPENTER_OPERATOR_IMAGE_OVERRIDE: registry.ci.openshift.org/ocp/5.1@sha256:<digest>
```

Non-latest tags use `IfNotPresent`, so even a recreated Pod can reuse a cached
image after a tag moves. Verifying the Deployment's tag alone does not identify
the build that ran.

CI must reject an empty image dependency and verify that the KO Deployment uses
the intended image. Otherwise, a missing override could silently select the pin
or payload instead.

### Pinned compatibility and HO release validation

Leave the environment override unset and avoid HostedCluster image annotations.
These tests must exercise the KO build paired with the HO build under test,
not an injected development image.

Check that the candidate HO build contains a nonempty pin and verify that the
KO Deployment uses that pullspec. If the pin is empty, tests exercise the
payload fallback rather than a pinned HO/KO pair.

### Per-cluster development and debugging

Set the `hypershift.openshift.io/karpenter-operator-image` HostedCluster annotation
when only one cluster needs a different image. It takes precedence over the
HO-wide environment override and pin. Like other image annotations, this
pullspec is not rewritten by `--registry-overrides`; specify the mirror directly
when needed.

## Image precedence and fallback

Standalone KO deployments use the first configured image:

1. The nonempty HostedCluster image annotation.
2. The nonempty `KARPENTER_OPERATOR_IMAGE_OVERRIDE` environment variable on HO.
3. The `karpenterOperatorImage` constant in `overrides.go`.
4. The `karpenter-operator` image from the hosted cluster's release payload.

An unset or empty environment variable means no environment override. An empty
pin constant falls back to the payload. Payload consumption is a compatibility
fallback, not the intended HCP release mechanism or CI image-selection method.

Nonempty environment overrides and pins must be valid, registry-qualified
pullspecs. A pin must also include a digest. Bare names such as
`karpenter-operator` are rejected for these sources so workload defaulting cannot
reinterpret them as payload keys. Invalid configuration returns an error rather
than falling through to a lower-priority image. Higher-priority overrides bypass
validation of unused lower-priority images. Image pull failures do not trigger
fallback.

## Disconnected registries

HO's `--registry-overrides` map applies to the selected environment override or
pin using the same registry-prefix matching as payload images.
For example, `--registry-overrides=registry.redhat.io=mirror.internal:5000`
redirects a pin in `registry.redhat.io/openshift5/` to the corresponding path in
`mirror.internal:5000/`, preserving its digest. The image must already be
mirrored and the management cluster must have credentials for the mirror.

Source selection happens before remapping. Environment overrides and pins are
remapped once, and the result must still meet the registry and digest validation
requirements. Annotation pullspecs are not remapped. Payload fallback uses the
already-remapped release image without remapping it again. A directly configured
mirrored pullspec is also supported.

## Updating the pin

Set `karpenterOperatorImage` in `overrides.go` to the full pullspec of an
ART-promoted HCP build, including its registry and digest. The constant is
compiled into HO. Commit the bump and make sure AutoNode presubmits pass before
merging.

A digest bump rolls the KO Deployments consuming the pin when the HO image
containing that pin is deployed.
