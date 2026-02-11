

## API Review: `DNS_Resolver` field in `NodePoolSpec`

I've thoroughly reviewed your proposed API change against the existing HyperShift API conventions, Kubernetes API conventions, and OpenShift API guidelines. Here are my findings:

---

### ❌ Issue 1: Field Naming — `DNS_Resolver` violates Go and Kubernetes conventions

**Problem:** Go uses `CamelCase` for exported field names, and Kubernetes APIs use `lowerCamelCase` for JSON serialization. Underscores are never used in either.

**Fix:**
```go
// Go field name:   DNSResolver  (or CustomDNSServer, DNSNameserver, etc.)
// JSON tag:        "dnsResolver"
```

**References:**
- [Effective Go — MixedCaps](https://go.dev/doc/effective_go#mixed-caps): "the convention in Go is to use MixedCaps or mixedCaps rather than underscores"
- [Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#naming-conventions): JSON field names must be `lowerCamelCase`
- [OpenShift API conventions](https://github.com/openshift/enhancements/blob/master/dev-guide/api-conventions.md): Same requirement

---

### ❌ Issue 2: Missing `omitempty` and `+optional` marker

**Problem:** This is clearly an optional field (not every NodePool needs a custom DNS resolver), but it's missing both the `omitempty` JSON tag and the `+optional` API marker. Every optional field in the existing `NodePoolSpec` uses these consistently — e.g. `Replicas`, `AutoScaling`, `PausedUntil`, `NodeDrainTimeout`.

**Fix:**
```go
// +optional
DNSResolver *string `json:"dnsResolver,omitempty"`
```

---

### ❌ Issue 3: Should be a pointer type (`*string`), not a bare `string`

**Problem:** In Kubernetes API conventions, optional scalar fields should be pointers so that the zero value (`""`) is distinguishable from "not set." The existing NodePoolSpec follows this pattern — see `PausedUntil *string`, `Replicas *int32`, `NodeDrainTimeout *metav1.Duration`.

**Fix:** Use `*string` as shown above.

---

### ❌ Issue 4: Missing kubebuilder validation markers

**Problem:** IP addresses should be validated. The existing codebase consistently uses kubebuilder validation markers for IP/DNS fields. For example:
- `AdvertiseAddress` in `APIServerNetworking` uses `+kubebuilder:validation:MaxLength=255`
- `DNSNameservers` in OpenStack's `SubnetSpec` uses `+kubebuilder:validation:items:MaxLength=45` (for IPv6)

**Fix:** Add format/length validation:
```go
// +kubebuilder:validation:MaxLength=45
// +kubebuilder:validation:MinLength=1
```
Or even better, use a pattern to enforce a valid IP:
```go
// +kubebuilder:validation:Pattern=`^(\d{1,3}\.){3}\d{1,3}$`
```
(Expand for IPv6 if needed.)

---

### ❌ Issue 5: Comment format doesn't follow OpenShift API conventions

**Problem:** OpenShift API conventions require:
1. Comments start with the **Go field name** (e.g., `// dnsResolver is...`), using the **lowerCamelCase JSON name** per OpenShift conventions
2. No underscores in description references
3. Clear description of behavior, default, and optionality

**Fix:**
```go
// dnsResolver is the IP address of a custom DNS server that nodes in this
// NodePool will use instead of the default cluster DNS.
// When not set, nodes use the default cluster DNS configuration.
// +optional
// +kubebuilder:validation:MaxLength=45
// +kubebuilder:validation:MinLength=1
DNSResolver *string `json:"dnsResolver,omitempty"`
```

---

### ⚠️ Issue 6: Design consideration — Is `NodePoolSpec` the right place?

The existing codebase already has DNS-related configuration at:
- **`HostedCluster.Spec.DNS`** — cluster-wide DNS (base domain, zones)
- **`OpenStackPlatform.SubnetSpec.DNSNameservers`** — platform-specific DNS nameservers

Consider:
1. **Is this platform-specific?** If only relevant for certain platforms (e.g., AWS, Azure), it may belong in the platform-specific node pool config (e.g., `AWSNodePoolPlatform`).
2. **Should it support multiple resolvers?** Most DNS configurations allow a list of nameservers for redundancy. Consider `[]string` instead of a single `*string`, similar to the OpenStack `DNSNameservers` pattern.
3. **What consumes this?** How will the value propagate to nodes? (Ignition config, MachineConfig, cloud-init?) This affects where in the API the field belongs.

---

### ✅ Recommended Implementation

```go
type NodePoolSpec struct {
    // ...existing fields...

    // dnsResolver specifies custom DNS nameserver IP addresses for nodes
    // in this NodePool. When set, nodes will use these DNS servers instead
    // of the default cluster DNS configuration.
    // +optional
    // +kubebuilder:validation:MaxItems=3
    // +kubebuilder:validation:items:MaxLength=45
    // +kubebuilder:validation:items:MinLength=1
    DNSResolvers []string `json:"dnsResolvers,omitempty"`
}
```

Or if you intentionally want only a single resolver:

```go
type NodePoolSpec struct {
    // ...existing fields...

    // dnsResolver is the IP address of a custom DNS server for nodes in
    // this NodePool. When set, nodes will use this DNS server instead of
    // the default cluster DNS configuration.
    // +optional
    // +kubebuilder:validation:MaxLength=45
    // +kubebuilder:validation:MinLength=1
    DNSResolver *string `json:"dnsResolver,omitempty"`
}
```

---

### Summary of Required Changes

| # | Issue | Severity | Fix |
|---|-------|----------|-----|
| 1 | `DNS_Resolver` uses underscores | **Blocking** | Rename to `DNSResolver` / `"dnsResolver"` |
| 2 | Missing `omitempty` + `+optional` | **Blocking** | Add both |
| 3 | Bare `string` instead of `*string` | **Blocking** | Use pointer type |
| 4 | No validation markers | **High** | Add `MaxLength`, consider `Pattern` |
| 5 | Comment format incorrect | **Medium** | Follow OpenShift API comment conventions |
| 6 | Single vs. multiple resolvers | **Design** | Consider `[]string` for resilience |

After making changes to the API types, you'll need to run:
```bash
make api        # Regenerate CRDs
make verify     # Full verification
make lint-fix   # Auto-fix lint issues
```
