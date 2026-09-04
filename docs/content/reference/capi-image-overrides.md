# CAPI Provider Image Overrides

## Overview

HyperShift uses Cluster API (CAPI) providers to manage infrastructure for hosted clusters. The CAPI provider images used in the hosted control plane are resolved through a layered override mechanism. This document describes how CAPI provider images are selected, which platforms have overrides, and a backward compatibility pinning mechanism active on specific release branches.

## Image Resolution Priority

For each platform, the CAPI provider image is resolved in the following order (lowest to highest priority):

1. **Payload image** -- from the hosted cluster's OCP release payload (via `platform.go` `GetPlatform()`)
2. **Environment variable override** -- from the HyperShift operator's own image references (set via `support/images/envvars.go`), meaning the image version is determined by the HyperShift operator, **not** the hosted cluster's payload
3. **Annotation override** -- explicit per-HostedCluster annotation (always wins)

When multiple sources are present, the highest-priority source takes effect. If no override is set, the image falls back to the next lower priority level.

!!! note
    KubeVirt and Agent are exceptions to this priority chain -- they do not have a payload image. Their base image is a hardcoded default (set in code), which the env var and annotation can then override.

## Per-Platform Behavior

| Platform | Env Var | Annotation | Payload Image Used? | Override Behavior | First Branch |
|----------|---------|------------|---------------------|-------------------|--------------|
| AWS | `IMAGE_AWS_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-aws-image` | Yes (payload >= 4.12) | Env var only overrides for `payloadVersion < 4.12` (version-gated) | release-4.14+ |
| Azure | `IMAGE_AZURE_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-azure-image` | Yes, but always overridden | Env var always overrides (no version check) | release-4.14+ |
| GCP | `IMAGE_GCP_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-gcp-image` | Yes, but always overridden | Env var always overrides | release-4.22+ (stub on 4.21) |
| OpenStack | `IMAGE_OPENSTACK_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-openstack-image` | Yes, but always overridden | Env var always overrides | release-4.17+ |
| PowerVS | `IMAGE_POWERVS_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-powervs-image` | Yes, but always overridden | Env var always overrides | release-4.14+ |
| KubeVirt | `IMAGE_KUBEVIRT_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-kubevirt-image` | No (never from payload) | Always from env var or hardcoded default | release-4.14+ |
| Agent | `IMAGE_AGENT_CAPI_PROVIDER` | `hypershift.openshift.io/capi-provider-agent-image` | No (hardcoded default `quay.io/edge-infrastructure/cluster-api-provider-agent:latest`) | Always from env var or hardcoded default | release-4.14+ |

### How Environment Variable Overrides Work

The environment variables listed above (e.g. `IMAGE_AZURE_CAPI_PROVIDER`) are set on the HyperShift operator Deployment by the installation tooling. They are populated from the HyperShift operator's own image references file (`support/images/envvars.go`), which maps OCP release payload image names to environment variables.

In all standard installation methods -- including MCE (Multicluster Engine) and the `hypershift install` CLI -- these env vars are set automatically. When an env var is present, it takes precedence over the payload image. The practical effect is that the CAPI provider version is determined by the **HyperShift operator version**, not the hosted cluster's OCP payload version.

!!! note
    AWS is the only platform where the hosted cluster's OCP payload determines the CAPI provider image (for payloads >= 4.12). For all other platforms, the image is always determined by the HyperShift operator.

## Backward Compatibility: CAPI v1beta2 Image Pinning

### Background

Starting with OCP 4.21, the upstream CAPI v1.11 bump introduced the `v1beta2` API version. Since HyperShift does not yet support CAPI `v1beta2`, a backward compatibility mechanism pins specific CAPI images to their 4.20.10 equivalents (which ship CAPI v1.10 / `v1beta1` only).

### Implementation

The pinning is implemented in `support/backwardcompat/backwardcompat.go` via the `GetBackwardCompatibleCAPIImage()` function. For hosted clusters with payload version >= 4.21.0, this function extracts the CAPI images from a pinned 4.20.10 release instead of the hosted cluster's own payload.

Pinned release:

```text
quay.io/openshift-release-dev/ocp-release@sha256:7f183e9b5610a2c9f9aabfd5906b418adfbe659f441b019933426a19bf6a5962
```

This corresponds to the `4.20.10-multi` release.

### Affected Components

The pinning applies to these three components only:

- **`cluster-capi-controllers`** (core CAPI manager) -- overridden in `hostedcluster_controller.go`
- **`aws-cluster-api-controllers`** (CAPA) -- overridden in `platform.go`
- **`azure-cluster-api-controllers`** (CAPZ) -- overridden in `platform.go`

The following platforms are **not affected** by the pinning: PowerVS, OpenStack, GCP, KubeVirt, Agent.

### Branch Status

| Branch | Pinning Active? | Pinned Components | Notes |
|--------|-----------------|-------------------|-------|
| release-4.20 | No | -- | Not needed -- already ships CAPI v1.10 |
| release-4.21 | Yes | `cluster-capi-controllers`, CAPA (AWS), CAPZ (Azure) | Pins to 4.20.10 for payloads >= 4.21 |
| release-4.22 | Yes | `cluster-capi-controllers`, CAPA (AWS), CAPZ (Azure) | Pins to 4.20.10 for payloads >= 4.21 |
| release-5.0 | No | -- | Pinning removed -- CAPI bumped to v1.11 (CNTRLPLANE-2207) |
| main | No | -- | Pinning removed -- HyperShift compiles against CAPI v1.11+ (CNTRLPLANE-2207) |

!!! note
    The pinning was removed once HyperShift gained the ability to compile with CAPI v1.11+, tracked under CNTRLPLANE-2207. The related `v1beta2` client migration is tracked separately under CNTRLPLANE-1200. Release 5.0 and all future versions will **not** have this pinning.

### Introducing PRs

- [OCPBUGS-74247: CAPI image overrides aware of registry config](https://github.com/openshift/hypershift/pull/7575) -- initial implementation (merged to main)
- [OCPBUGS-86295: CAPI image overrides aware of registry config](https://github.com/openshift/hypershift/pull/8559) -- backport to release-4.21

### Known Issues

In disconnected environments, the 4.20.10 images are not part of the 4.21/4.22 payload's `image-references`, so `oc-mirror` does not discover them automatically. Users must manually mirror the 4.20.10 release.

Tracked under OCPBUGS-74263 and OCPBUGS-86056.

## Related Files

- `support/backwardcompat/backwardcompat.go` -- backward compatibility image pinning (release-4.21, release-4.22)
- `hypershift-operator/controllers/hostedcluster/internal/platform/platform.go` -- `GetPlatform()` payload image lookup
- `hypershift-operator/controllers/hostedcluster/internal/platform/{aws,azure,gcp,kubevirt,agent,openstack,powervs}/` -- per-platform `CAPIProviderDeploymentSpec()`
- `support/images/envvars.go` -- env var to payload image name mapping
- `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go` -- CAPI manager image override
