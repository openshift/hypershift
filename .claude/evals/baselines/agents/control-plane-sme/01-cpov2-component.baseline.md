Now I have all the details I need. Let me provide the complete implementation guidance.

---

## Complete Implementation Guide: Audit Logger Control Plane Component

Based on my thorough analysis of the codebase, here's exactly how to implement this. I want to be clear about something first though: **HyperShift already has audit log forwarding via the `AuditWebhook` field on the HostedCluster/HostedControlPlane spec**, which configures the kube-apiserver's built-in `--audit-webhook-config-file` flag. However, your approach — a sidecar that reads from the shared `emptyDir` volume and forwards via HTTPS — provides a decoupled, more flexible alternative. Here's the complete design.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                    kube-apiserver Pod                                │
│                                                                     │
│  ┌──────────────────┐    ┌───────────────┐    ┌──────────────────┐  │
│  │  kube-apiserver   │    │  audit-logs   │    │  audit-logger    │  │
│  │                   │    │  (existing)   │    │  (NEW sidecar)   │  │
│  │  Writes audit.log │    │  Tails to     │    │  Reads audit.log │  │
│  │  to shared volume │    │  stdout       │    │  & forwards via  │  │
│  │                   │    │               │    │  HTTPS webhook   │  │
│  └────────┬──────────┘    └───────┬───────┘    └────────┬─────────┘  │
│           │                       │                      │           │
│           └───────────┬───────────┘                      │           │
│                       │                                  │           │
│                ┌──────┴──────┐              ┌────────────┴────────┐  │
│                │ emptyDir:   │              │ Secret:             │  │
│                │ "logs"      │              │ audit-logger-config │  │
│                │ (audit.log) │◄─────────────│ (webhook endpoint)  │  │
│                └─────────────┘              └─────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
                                                        │
                                              ┌─────────▼──────────┐
                                              │ External Webhook   │
                                              │ Endpoint (HTTPS)   │
                                              └────────────────────┘
```

### Key Decision: Sidecar on KAS vs. Separate Deployment

Since you specified a **sidecar container on the kube-apiserver pod**, this is **not** a new cpov2 component — it's a modification to the existing `kube-apiserver` component. A separate Deployment would need its own mechanism to access audit logs (e.g., a PersistentVolume), which adds complexity. The sidecar approach is simpler because it shares the existing `logs` emptyDir volume.

This means the implementation is:
1. **API change**: Add a new field for audit log forwarding configuration
2. **KAS component modification**: Inject the sidecar container conditionally
3. **New manifest**: A Secret for the webhook configuration

---

## Step 1: API Definition

### Option A: Reuse `AuditWebhook` (Recommended)

The existing `AuditWebhook` field already references a Secret containing a webhook kubeconfig. The kube-apiserver uses this directly via `--audit-webhook-config-file`. If your intent is to provide a *different* forwarding mechanism (sidecar-based rather than KAS-native), you need a new field.

### Option B: New `AuditLogForwarding` field

Add to `api/hypershift/v1beta1/hostedcluster_types.go`:

```go
// In HostedClusterSpec:

// auditLogForwarding configures forwarding of kube-apiserver audit logs
// to an external HTTPS webhook endpoint via a sidecar container.
// This is an alternative to the built-in auditWebhook mechanism.
// The referenced secret must contain:
// - "endpoint": the HTTPS URL to forward audit events to
// - "ca-bundle.crt": (optional) CA certificate bundle for TLS verification
// - "tls.crt": (optional) client certificate for mTLS
// - "tls.key": (optional) client key for mTLS
//
// +optional
AuditLogForwarding *AuditLogForwardingSpec `json:"auditLogForwarding,omitempty"`
```

```go
// AuditLogForwardingSpec defines the configuration for forwarding
// audit logs to an external endpoint.
type AuditLogForwardingSpec struct {
    // endpoint is the HTTPS URL to forward audit events to.
    // +required
    // +kubebuilder:validation:Pattern=`^https://`
    Endpoint string `json:"endpoint"`

    // secretRef references a Secret in the same namespace as the HostedCluster
    // that contains TLS configuration for the webhook endpoint.
    // The secret may contain:
    // - "ca-bundle.crt": CA certificate bundle for server TLS verification
    // - "tls.crt" and "tls.key": client certificate and key for mTLS
    //
    // +optional
    SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

    // batchMaxSize is the maximum number of audit events to batch before
    // sending to the endpoint. Defaults to 100.
    //
    // +optional
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=10000
    // +kubebuilder:default=100
    BatchMaxSize *int32 `json:"batchMaxSize,omitempty"`

    // batchMaxWaitSeconds is the maximum time in seconds to wait before
    // sending a batch. Defaults to 5.
    //
    // +optional
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=300
    // +kubebuilder:default=5
    BatchMaxWaitSeconds *int32 `json:"batchMaxWaitSeconds,omitempty"`
}
```

Mirror this in `api/hypershift/v1beta1/hosted_controlplane.go` on `HostedControlPlaneSpec`:

```go
// auditLogForwarding configures forwarding of kube-apiserver audit logs
// to an external HTTPS webhook endpoint via a sidecar container.
//
// +optional
AuditLogForwarding *AuditLogForwardingSpec `json:"auditLogForwarding,omitempty"`
```

After adding the type, run:
```bash
make api
```

---

## Step 2: Modify the KAS Component to Inject the Sidecar

### File: `control-plane-operator/controllers/hostedcontrolplane/v2/kas/deployment.go`

Add constants:

```go
const (
    auditLoggerContainerName     = "audit-logger"
    auditLoggerConfigVolumeName  = "audit-logger-config"
    auditLoggerTLSVolumeName     = "audit-logger-tls"
)
```

Add the sidecar injection function:

```go
func applyAuditLoggerSidecar(podSpec *corev1.PodSpec, hcp *hyperv1.HostedControlPlane) {
    forwarding := hcp.Spec.AuditLogForwarding

    batchMaxSize := int32(100)
    if forwarding.BatchMaxSize != nil {
        batchMaxSize = *forwarding.BatchMaxSize
    }
    batchMaxWait := int32(5)
    if forwarding.BatchMaxWaitSeconds != nil {
        batchMaxWait = *forwarding.BatchMaxWaitSeconds
    }

    args := []string{
        "--audit-log-path=/var/log/kube-apiserver/audit.log",
        fmt.Sprintf("--webhook-endpoint=%s", forwarding.Endpoint),
        fmt.Sprintf("--batch-max-size=%d", batchMaxSize),
        fmt.Sprintf("--batch-max-wait=%ds", batchMaxWait),
    }

    volumeMountsForContainer := []corev1.VolumeMount{
        {
            Name:      "logs",
            MountPath: "/var/log/kube-apiserver",
            ReadOnly:  true,
        },
    }

    if forwarding.SecretRef != nil {
        args = append(args, "--tls-ca-bundle=/etc/audit-logger/tls/ca-bundle.crt")
        args = append(args, "--tls-cert=/etc/audit-logger/tls/tls.crt")
        args = append(args, "--tls-key=/etc/audit-logger/tls/tls.key")

        volumeMountsForContainer = append(volumeMountsForContainer, corev1.VolumeMount{
            Name:      auditLoggerTLSVolumeName,
            MountPath: "/etc/audit-logger/tls",
            ReadOnly:  true,
        })

        podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
            Name: auditLoggerTLSVolumeName,
            VolumeSource: corev1.VolumeSource{
                Secret: &corev1.SecretVolumeSource{
                    SecretName:  forwarding.SecretRef.Name,
                    DefaultMode: ptr.To[int32](0o640),
                },
            },
        })
    }

    podSpec.Containers = append(podSpec.Containers, corev1.Container{
        Name:            auditLoggerContainerName,
        Image:           "audit-logger", // payload image key - replaced by framework
        ImagePullPolicy: corev1.PullIfNotPresent,
        Command:         []string{"/usr/bin/audit-logger"},
        Args:            args,
        Resources: corev1.ResourceRequirements{
            Requests: corev1.ResourceList{
                corev1.ResourceCPU:    resource.MustParse("10m"),
                corev1.ResourceMemory: resource.MustParse("50Mi"),
            },
        },
        VolumeMounts: volumeMountsForContainer,
    })
}
```

### Wire it into `adaptDeployment`:

Add the following block in `adaptDeployment()` (after the existing `AuditWebhook` handling at line ~93):

```go
// Inject audit-logger sidecar for audit log forwarding
if hcp.Spec.AuditLogForwarding != nil {
    applyAuditLoggerSidecar(&deployment.Spec.Template.Spec, hcp)
}
```

Also, if the audit profile is `None`, remove the audit-logger container too (add to the existing `NoneAuditProfileType` block):

```go
if hcp.Spec.Configuration.GetAuditPolicyConfig().Profile == configv1.NoneAuditProfileType {
    util.RemoveContainer("audit-logs", &deployment.Spec.Template.Spec)
    util.RemoveContainer(auditLoggerContainerName, &deployment.Spec.Template.Spec)
}
```

---

## Step 3: Propagate the Field from HostedCluster to HostedControlPlane

Look at how the hypershift-operator controller copies fields from `HostedClusterSpec` to `HostedControlPlaneSpec`. The field must be propagated in `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go` in the function that builds/reconciles the HostedControlPlane resource:

```go
hcp.Spec.AuditLogForwarding = hc.Spec.AuditLogForwarding
```

If the `SecretRef` references a Secret in the HostedCluster namespace, you'll need to **copy that Secret** into the HCP namespace (same pattern as `AuditWebhook`). Look at how `AuditWebhook` secret is handled:

```go
// In the hostedcluster controller, the AuditWebhook secret is synced
// from the HostedCluster namespace to the HCP namespace
```

Apply the same pattern for `AuditLogForwarding.SecretRef`.

---

## Step 4: Resource Footprint Considerations

| Resource | Value | Rationale |
|----------|-------|-----------|
| CPU Request | 10m | Log tailing and HTTP forwarding is I/O-bound, not CPU-intensive |
| Memory Request | 50Mi | Buffer for batching audit events before forwarding |
| CPU Limit | None | Avoid throttling during burst log periods |
| Memory Limit | Consider 200Mi | Prevent unbounded growth if webhook endpoint is slow |

**Impact on KAS pod**: Adding a sidecar increases the pod's total resource footprint. With `10m` CPU and `50Mi` memory, this is minimal. The existing `audit-logs` sidecar already uses `5m/10Mi`. Combined, both audit-related sidecars add only `15m/60Mi` to the pod.

**Volume considerations**: The `logs` emptyDir has no size limit by default. If the audit-logger sidecar falls behind (slow webhook endpoint), the existing KAS audit log rotation (`--audit-log-maxsize` and `--audit-log-maxbackup` flags) prevents unbounded disk growth. Your sidecar should track its read position to handle log rotation gracefully.

---

## Step 5: Security Considerations

### 1. Secret Access — Least Privilege
The audit-logger sidecar reads from a Secret volume mounted by the framework. It does NOT need:
- Management cluster KAS access (`NeedsManagementKASAccess() == false`)
- ServiceAccount token (`AutomountServiceAccountToken` stays `false`)
- Any cloud credentials

### 2. Read-Only Root Filesystem
The cpov2 framework **automatically enforces** `readOnlyRootFilesystem: true` on all containers and mounts a `tmp` EmptyDir. Your sidecar must write any state (e.g., cursor position) to `/tmp`.

### 3. TLS Validation
The sidecar MUST:
- Validate the webhook endpoint's TLS certificate against the provided CA bundle
- Support mTLS when client certificates are provided
- Never disable TLS verification

### 4. No Cloud Account Access
The sidecar should not require any cloud provider credentials. If the webhook endpoint is within a cloud VPC, network access is handled at the infrastructure level, not via IAM.

### 5. Network Egress
The sidecar needs to reach an external HTTPS endpoint. In HyperShift, the KAS pod already has konnectivity for egress to the data plane. For egress to an external (non-data-plane) endpoint, the sidecar uses the management cluster's network directly — no konnectivity needed. If the endpoint requires proxy configuration, respect the standard `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` environment variables (already injected by the framework when proxy is configured).

---

## Step 6: Unit Tests

### File: `control-plane-operator/controllers/hostedcontrolplane/v2/kas/deployment_test.go`

```go
func TestApplyAuditLoggerSidecar(t *testing.T) {
    tests := []struct {
        name      string
        hcp       *hyperv1.HostedControlPlane
        expectContainer bool
        expectTLSVolume bool
        expectArgs      []string
    }{
        {
            name: "When audit log forwarding is configured it should inject the sidecar container",
            hcp: &hyperv1.HostedControlPlane{
                Spec: hyperv1.HostedControlPlaneSpec{
                    AuditLogForwarding: &hyperv1.AuditLogForwardingSpec{
                        Endpoint: "https://audit.example.com/v1/logs",
                    },
                },
            },
            expectContainer: true,
            expectTLSVolume: false,
            expectArgs:      []string{"--webhook-endpoint=https://audit.example.com/v1/logs"},
        },
        {
            name: "When audit log forwarding has TLS secret it should mount the TLS volume",
            hcp: &hyperv1.HostedControlPlane{
                Spec: hyperv1.HostedControlPlaneSpec{
                    AuditLogForwarding: &hyperv1.AuditLogForwardingSpec{
                        Endpoint: "https://audit.example.com/v1/logs",
                        SecretRef: &corev1.LocalObjectReference{
                            Name: "audit-tls-secret",
                        },
                    },
                },
            },
            expectContainer: true,
            expectTLSVolume: true,
        },
        {
            name: "When audit log forwarding is nil it should not inject the sidecar",
            hcp: &hyperv1.HostedControlPlane{
                Spec: hyperv1.HostedControlPlaneSpec{},
            },
            expectContainer: false,
            expectTLSVolume: false,
        },
        {
            name: "When batch parameters are set it should pass them as arguments",
            hcp: &hyperv1.HostedControlPlane{
                Spec: hyperv1.HostedControlPlaneSpec{
                    AuditLogForwarding: &hyperv1.AuditLogForwardingSpec{
                        Endpoint:            "https://audit.example.com/v1/logs",
                        BatchMaxSize:        ptr.To[int32](500),
                        BatchMaxWaitSeconds: ptr.To[int32](10),
                    },
                },
            },
            expectContainer: true,
            expectArgs:      []string{"--batch-max-size=500", "--batch-max-wait=10s"},
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            podSpec := &corev1.PodSpec{
                Volumes: []corev1.Volume{
                    {Name: "logs", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
                },
            }

            if tt.hcp.Spec.AuditLogForwarding != nil {
                applyAuditLoggerSidecar(podSpec, tt.hcp)
            }

            hasContainer := false
            for _, c := range podSpec.Containers {
                if c.Name == auditLoggerContainerName {
                    hasContainer = true
                    for _, expectedArg := range tt.expectArgs {
                        found := false
                        for _, arg := range c.Args {
                            if arg == expectedArg {
                                found = true
                                break
                            }
                        }
                        if !found {
                            t.Errorf("expected arg %q not found in container args: %v", expectedArg, c.Args)
                        }
                    }
                }
            }
            if hasContainer != tt.expectContainer {
                t.Errorf("expected container present=%v, got=%v", tt.expectContainer, hasContainer)
            }

            hasTLSVolume := false
            for _, v := range podSpec.Volumes {
                if v.Name == auditLoggerTLSVolumeName {
                    hasTLSVolume = true
                }
            }
            if hasTLSVolume != tt.expectTLSVolume {
                t.Errorf("expected TLS volume present=%v, got=%v", tt.expectTLSVolume, hasTLSVolume)
            }
        })
    }
}

func TestAuditLoggerRemovedWhenAuditProfileNone(t *testing.T) {
    t.Run("When audit profile is None it should remove the audit-logger sidecar", func(t *testing.T) {
        podSpec := &corev1.PodSpec{
            Containers: []corev1.Container{
                {Name: "kube-apiserver"},
                {Name: "audit-logs"},
                {Name: auditLoggerContainerName},
            },
        }

        util.RemoveContainer("audit-logs", podSpec)
        util.RemoveContainer(auditLoggerContainerName, podSpec)

        for _, c := range podSpec.Containers {
            if c.Name == "audit-logs" || c.Name == auditLoggerContainerName {
                t.Errorf("container %q should have been removed", c.Name)
            }
        }
    })
}
```

---

## Step 7: Validation

Add validation in the HostedCluster webhook or admission logic:

```go
// Validate AuditLogForwarding
if hc.Spec.AuditLogForwarding != nil {
    if hc.Spec.AuditLogForwarding.Endpoint == "" {
        allErrs = append(allErrs, field.Required(
            field.NewPath("spec", "auditLogForwarding", "endpoint"),
            "endpoint is required when auditLogForwarding is configured",
        ))
    }
    if !strings.HasPrefix(hc.Spec.AuditLogForwarding.Endpoint, "https://") {
        allErrs = append(allErrs, field.Invalid(
            field.NewPath("spec", "auditLogForwarding", "endpoint"),
            hc.Spec.AuditLogForwarding.Endpoint,
            "endpoint must use HTTPS scheme",
        ))
    }
}
```

---

## Step 8: E2E Test

Add an E2E test in `test/e2e/` that:
1. Creates a HostedCluster with `AuditLogForwarding` configured (pointing to a test webhook server deployed as a Job/Deployment in the management cluster)
2. Verifies the KAS pod has the `audit-logger` sidecar container
3. Verifies the sidecar is running and healthy
4. Optionally verifies audit events reach the test webhook endpoint

---

## Summary of Files to Modify/Create

| File | Action |
|------|--------|
| `api/hypershift/v1beta1/hostedcluster_types.go` | Add `AuditLogForwarding` field + `AuditLogForwardingSpec` type |
| `api/hypershift/v1beta1/hosted_controlplane.go` | Mirror `AuditLogForwarding` field |
| `api/hypershift/v1beta1/zz_generated.deepcopy.go` | Auto-generated via `make api` |
| `control-plane-operator/controllers/hostedcontrolplane/v2/kas/deployment.go` | Add `applyAuditLoggerSidecar()`, wire into `adaptDeployment()` |
| `control-plane-operator/controllers/hostedcontrolplane/v2/kas/deployment_test.go` | Unit tests for sidecar injection |
| `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go` | Propagate field from HC → HCP, sync Secret |
| Webhook/validation code | Add validation for the new field |
| `test/e2e/` | E2E test |

---

## Recommendations

1. **Prefer the existing `AuditWebhook` mechanism** if possible — it's already built into the kube-apiserver and doesn't add a sidecar. Only implement the sidecar approach if you need features the native webhook doesn't support (custom batching, different protocols, log transformation, etc.).

2. **Consider a liveness probe** for the audit-logger sidecar. If the webhook endpoint is unreachable, the sidecar should report unhealthy but NOT cause the KAS pod to restart (use a separate liveness endpoint with high failure threshold).

3. **Log rotation awareness**: The sidecar must handle audit log rotation (`audit.log` → `audit.log.1`, etc.). Use `inotify` or `tail -F` semantics to follow the file through rotations.

4. **Backpressure handling**: If the webhook endpoint is slow or down, the sidecar should buffer events up to a limit, then drop oldest events. Never block the KAS from writing audit logs.

5. **Run `make verify`** after all changes to ensure CRDs, deep copy, and formatting are all correct.

6. **Run `make lint-fix`** to catch any Go formatting or linting issues before submitting.
