# CAPI Provider Image Overrides

## Overview

HyperShift uses Cluster API (CAPI) providers to manage infrastructure for hosted clusters. The CAPI provider images used in the hosted control plane are resolved through a layered override mechanism. This document describes how CAPI provider images are selected, which platforms have overrides, and a backward compatibility pinning mechanism active on specific release branches.

## Image Resolution Priority

For each platform, the CAPI provider image is resolved in the following order (lowest to highest priority):

1. **Payload image** -- from the hosted cluster's OCP release payload (via `platform.go` `GetPlatform()`)
2. **Environment variable override** -- from the HyperShift operator's own image references (set via `support/images/envvars.go`), meaning the image version is determined by the HyperShift operator, **not** the hosted cluster's payload
3. **Annotation override** -- explicit per-HostedCluster annotation (always wins)

When multiple sources are present, the highest-priority source takes effect. If no override is set, the image falls back to the next lower priority level.

!!! note "Agent"
    Agent does not use a payload image. It has a hardcoded default (`quay.io/edge-infrastructure/cluster-api-provider-agent:latest`), which the env var and annotation can then override.

!!! warning "KubeVirt"
    KubeVirt does not use a payload image and has **no fallback default**. If neither the env var (`IMAGE_KUBEVIRT_CAPI_PROVIDER`) nor the annotation is set, the image resolution returns an error. The env var or annotation **must** be set for KubeVirt clusters to function.

## Per-Platform Behavior

The table below includes the core CAPI manager (`cluster-capi-controllers`) and all per-platform CAPI providers. The core manager is separate from the platform-specific providers -- it runs the shared CAPI controller logic, while each platform provider handles infrastructure-specific operations.

| Component | Env Var | Annotation | Payload Image Used? | Override Behavior | First Branch |
|-----------|---------|------------|---------------------|-------------------|--------------|
| Core CAPI manager | -- | `hypershift.openshift.io/capi-manager-image` | Yes (from payload) | Annotation overrides payload image; backward compat pinning applies (see below) | release-4.14+ |
| AWS | `IMAGE_AWS_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-aws-image` | Yes (payload >= 4.12) | Env var only overrides for `payloadVersion < 4.12` (version-gated) | release-4.14+ |
| Azure | `IMAGE_AZURE_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-azure-image` | Yes, but always overridden | Env var always overrides (no version check) | release-4.14+ |
| GCP | `IMAGE_GCP_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-gcp-image` | Yes, but always overridden | Env var always overrides | release-4.22+ (stub on 4.21) |
| OpenStack | `IMAGE_OPENSTACK_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-openstack-image` | Yes, but always overridden | Env var always overrides | release-4.17+ |
| PowerVS | `IMAGE_POWERVS_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-powervs-image` | Yes, but always overridden | Env var always overrides | release-4.14+ |
| KubeVirt | `IMAGE_KUBEVIRT_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-kubevirt-image` | No (never from payload) | Always from env var or annotation (no fallback -- errors if absent) | release-4.14+ |
| Agent | `IMAGE_AGENT_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-agent-image` | No (hardcoded default `quay.io/edge-infrastructure/cluster-api-provider-agent:latest`) | Always from env var or hardcoded default | release-4.14+ |

### How Environment Variable Overrides Work

The environment variables listed above (e.g. `IMAGE_AZURE_CAPI_PROVIDER`) are set on the HyperShift operator Deployment by the installation tooling. `support/images/envvars.go` defines the tag-to-environment-variable mapping (`TagMapping()`) shared by every installation path: a tag named after a CAPI image (for example `cluster-api-provider-kubevirt`) becomes the corresponding environment variable (`IMAGE_KUBEVIRT_CAPI_PROVIDER`).

Both installation paths populate these env vars automatically, but which platforms get one depends on the image references each path supplies. When an env var is present and applies to the platform (see the table above), it takes precedence over the payload image; when it is absent, the platform falls back to the payload image. The practical effect is that, for platforms with an env var, the CAPI provider version is determined by the **HyperShift operator version**, not the hosted cluster's OCP payload version.

!!! note
    When the installation sets env vars for every provider (for example, `hypershift install --image-refs` with a full OCP payload), AWS is the only platform where the hosted cluster's OCP payload determines the CAPI provider image (for payloads >= 4.12); for all other platforms the env var always wins. This is not the case on MCE, which sets env vars only for Agent and KubeVirt -- see [With MCE](#with-mce).

#### With `hypershift install`

`hypershift install --image-refs <file>` reads an ImageStream (for example, the `image-references` file from an OCP payload) and adds an environment variable to the HyperShift operator Deployment for every tag whose name appears in `TagMapping()`. Tags that are not in the mapping, such as `hypershift-operator`, are ignored by this step.

#### With MCE

MCE does not install the HyperShift operator from an OCP payload. Instead, the MCE release supplies a small ImageStream of its own, and the same `TagMapping()` turns its tags into environment variables:

1. The MCE `ClusterServiceVersion` pins each operand image, for example `OPERAND_IMAGE_CLUSTER_API_PROVIDER_KUBEVIRT`, and lists the same references under `relatedImages`.
2. The MCE operator (backplane-operator) renders the `hypershift-operator-imagestream` ConfigMap in the MCE namespace. Its ImageStream contains only three tags: `cluster-api-provider-agent`, `cluster-api-provider-kubevirt`, and `hypershift-operator`.
3. The HyperShift addon manager reads that ImageStream and installs the HyperShift operator with `hypershift install --image-refs`, which sets the environment variables. When the ImageStream changes, for example during an MCE upgrade, the addon manager updates the environment variables on the existing operator Deployment.

Only the Agent and KubeVirt providers are wired this way, because they are the only providers without an image in the hosted cluster's OCP release payload (Agent has a hardcoded fallback; KubeVirt has none). A default MCE installation therefore sets only `IMAGE_AGENT_CAPI_PROVIDER` and `IMAGE_KUBEVIRT_CAPI_PROVIDER`, and those two provider images follow the MCE release. The remaining providers -- AWS, Azure, GCP, OpenStack, and PowerVS -- are **not** set by MCE, so their env vars are absent and they resolve from the hosted cluster's OCP payload, subject to the version gating described above.

For example, MCE 2.17 pins the KubeVirt provider to a `registry.redhat.io/multicluster-engine/cluster-api-provider-kubevirt-rhel9` image by digest, and each 2.17 z-stream can pin a different digest. The Agent provider comes from `registry.redhat.io/multicluster-engine/cluster-api-provider-agent-rhel9` the same way.

To check the effective values on a management cluster:

```bash
oc get deployment -n hypershift operator \
  -o jsonpath='{range .spec.template.spec.containers[*].env[?(@.name=="IMAGE_KUBEVIRT_CAPI_PROVIDER")]}{.name}={.value}{"\n"}{end}'
```

On an MCE-managed cluster with the default configuration, only the Agent and KubeVirt provider env vars are present; `IMAGE_AWS_CAPI_PROVIDER`, `IMAGE_AZURE_CAPI_PROVIDER`, and the other provider env vars are absent.

A per-HostedCluster annotation still takes priority over the value set by the installer. To check whether a hosted cluster overrides the image:

```bash
oc get hostedcluster -n <namespace> <name> \
  -o jsonpath='{.metadata.annotations.hypershift\.openshift\.io/capi-provider-kubevirt-image}{"\n"}'
```

## Backward Compatibility: CAPI v1beta2 Image Pinning

### Background

Starting with OCP 4.21, the upstream CAPI v1.11 bump introduced the `v1beta2` API version. Since HyperShift did not support CAPI `v1beta2` until 5.0, a backward compatibility mechanism pins specific CAPI images to their 4.20.10 equivalents (which ship CAPI v1.10 / `v1beta1` only) for releases 4.21 and 4.22.

Separately, on `main` and `release-5.0+` (where HyperShift compiles against CAPI v1.11+), hosted clusters running OCP **below 4.21** ship a CAPI controller that writes status via `v1beta1`. The `v1beta1`→`v1beta2` conversion webhook drops the `status.phase` field, leaving it permanently empty. Additionally, on large environments this causes congestion on the API server due to high conversion volumes.
To fix this, the core CAPI manager image is pinned to a known-good OCP 4.22 build that writes status through `v1beta2` natively. This removes the need for conversion.

### Implementation

The pinning is implemented in `support/backwardcompat/backwardcompat.go` via the `GetBackwardCompatibleCAPIImage()` function. The behaviour depends on the branch and the hosted cluster's payload version:

- **release-4.21 / release-4.22** -- for payload version >= 4.21.0, the function extracts the CAPI images from a pinned 4.20.10 release instead of the hosted cluster's own payload.
- **main / release-5.0+** -- for payload version < 4.21.0, the function returns a pinned OCP 4.22 `cluster-capi-controllers` image. For versions >= 4.21.0 it returns an empty string and the standard image resolution priority applies.

In both cases an explicit `hypershift.openshift.io/capi-manager-image` annotation on the HostedCluster takes priority over the pinned image.

Pinned images:

```text
# release-4.21 / release-4.22 -- 4.20.10-multi release payload
quay.io/openshift-release-dev/ocp-release@sha256:7f183e9b5610a2c9f9aabfd5906b418adfbe659f441b019933426a19bf6a5962

# main / release-5.0+ -- cluster-capi-controllers component image from OCP 4.22
quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:c5c3e36db897fae332284e1a681044cd6997a70e8cb9059f810385352e7575ac
```

The OCP 4.22 component digest is stored directly in the function to avoid needing a pull-secret lookup or release payload extraction at reconciliation time.

### Affected Components

On **release-4.21 / release-4.22**, the pinning applies to these three components:

- **`cluster-capi-controllers`** (core CAPI manager) -- overridden in `hostedcluster_controller.go`
- **`aws-cluster-api-controllers`** (CAPA) -- overridden in `platform.go`
- **`azure-cluster-api-controllers`** (CAPZ) -- overridden in `platform.go`

On **main / release-5.0+**, only the core CAPI manager (`cluster-capi-controllers`) is pinned. Per-platform CAPI providers are not affected.

The following platforms are **not affected** by pinning on any branch: PowerVS, OpenStack, GCP, KubeVirt, Agent.

### Branch Status

| Branch | Pinning Active? | Pinned Components | Notes |
|--------|-----------------|-------------------|-------|
| release-4.20 | No | -- | Not needed -- already ships CAPI v1.10 |
| release-4.21 | Yes | `cluster-capi-controllers`, CAPA (AWS), CAPZ (Azure) | Pins to 4.20.10 for payloads >= 4.21 |
| release-4.22 | Yes | `cluster-capi-controllers`, CAPA (AWS), CAPZ (Azure) | Pins to 4.20.10 for payloads >= 4.21 |
| release-5.0+ | Yes | `cluster-capi-controllers` | Pins to OCP 4.22 CAPI manager for payloads < 4.21 |
| main | Yes | `cluster-capi-controllers` | Pins to OCP 4.22 CAPI manager for payloads < 4.21 |

!!! note
    The 4.20.10 pinning was removed once HyperShift gained the ability to compile with CAPI v1.11+, tracked under CNTRLPLANE-2207. The related `v1beta2` client migration is tracked separately under CNTRLPLANE-1200. On `main` and `release-5.0+` the 4.20.10 pinning is replaced by the < 4.21 phase-conversion + congestion avoidance pinning.

### Introducing PRs

- [OCPBUGS-74247: CAPI image overrides aware of registry config](https://github.com/openshift/hypershift/pull/7575) -- initial implementation (merged to main)
- [OCPBUGS-86295: CAPI image overrides aware of registry config](https://github.com/openshift/hypershift/pull/8559) -- backport to release-4.21
- [OCPBUGS-123171: CAPI manager pinning below 4.19 to fix phase conversion](https://github.com/openshift/hypershift/pull/9615) -- OCP 4.22 CAPI manager pinning for payloads < 4.19
- [OCPBUGS-140990: fix(capi): extend CAPI manager override up to 4.20](https://github.com/openshift/hypershift/pull/10306) -- OCP 4.22 CAPI manager pinning extended from < 4.19 to < 4.21

### Known Issues

In disconnected environments, pinned images are not part of the hosted cluster payload's `image-references`, so `oc-mirror` does not discover them automatically:

- On **release-4.21/4.22**: users must manually mirror the 4.20.10 release. Tracked under OCPBUGS-74263 and OCPBUGS-86056.
- On **main/5.0+**: users must manually mirror the OCP 4.22 `cluster-capi-controllers` image listed above. Tracked under OCPBUGS-123171 and OCPBUGS-140990.

## Core CAPI Manager Client Rate Limits

On `main`, the HyperShift operator starts the core CAPI manager (`cluster-capi-controllers`) with explicit client-side rate limits:

```text
--kube-api-qps=100
--kube-api-burst=200
--clustercache-client-qps=100
--clustercache-client-burst=200
```

`--kube-api-*` limit the manager's client for the management cluster API server. `--clustercache-client-*` limit the client it uses to reach the hosted cluster's API server.

The flags are added when either of these is true:

- the hosted cluster's payload version is 4.19 or later, or
- the manager runs the backward compatibility pinned image described above (payloads below 4.21 without an annotation override).

When `hypershift.openshift.io/capi-manager-image` sets any other image and the payload version is below 4.19, the flags are not added. CAPI manager builds from before OCP 4.19 may not register them. For example, the OCP 4.18 build only has the older `--clustercachetracker-client-*` flags and fails to start with `--clustercache-client-*`.

!!! warning
    Like the other version-gated CAPI manager arguments, from payload version 4.19 onwards these flags are added whatever image the annotation sets. For hosted clusters at payload version 4.19 or later, an image set with `hypershift.openshift.io/capi-manager-image` must be a `cluster-capi-controllers` build that registers these flags, such as the one shipped in the hosted cluster's own payload. An image that does not register them fails to start with an `unknown flag` error.

## Related Files

- `support/backwardcompat/backwardcompat.go` -- backward compatibility image pinning
- `hypershift-operator/controllers/hostedcluster/internal/platform/platform.go` -- `GetPlatform()` payload image lookup
- `hypershift-operator/controllers/hostedcluster/internal/platform/{aws,azure,gcp,kubevirt,agent,openstack,powervs}/` -- per-platform `CAPIProviderDeploymentSpec()`
- `support/images/envvars.go` -- env var to payload image name mapping (`TagMapping()`)
- `cmd/install/install.go` -- `hypershift install --image-refs` ImageStream parsing
- `cmd/install/assets/hypershift_operator.go` -- adds the CAPI image environment variables to the operator Deployment
- `stolostron/backplane-operator` `pkg/templates/charts/toggle/hypershift/templates/hypershift-addon-configmap.yaml` -- MCE `hypershift-operator-imagestream` (only the Agent and KubeVirt provider tags)
- `stolostron/hypershift-addon-operator` `pkg/install/hypershift.go` -- syncs the MCE ImageStream tags to the operator environment variables
- `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go` -- CAPI manager image override
- `control-plane-operator/controllers/hostedcontrolplane/v2/capi_manager/deployment.go` -- CAPI manager arguments, including the client rate limits
