# Contributing to HyperShift
Thank you for your interest in contributing to HyperShift! HyperShift enables running multiple OpenShift control planes as lightweight, cost-effective hosted clusters. Your contributions help improve this critical infrastructure technology.

The following guidelines will help ensure a smooth contribution process for both contributors and maintainers.

## Prior to Submitting a Pull Request
1. **Keep changes focused**: Scope commits to one thing and keep them minimal. Separate refactoring from logic changes, and save additional improvements for separate PRs.

2. **Test your changes**: Run `make pre-commit` to update dependencies, build code, verify formatting, and run tests. This prevents CI failures on your PR.

3. **Review before submitting**: Look at your changes from a reviewer's perspective and explain anything that might not be immediately clear in your PR description.

4. **Use proper commit format**: 
    1. Write commit subjects in [imperative mood](https://en.wikipedia.org/wiki/Imperative_mood) (e.g., "Fix bug" not "Fixed bug")
    2. Follow [conventional commit format](https://www.conventionalcommits.org/) and include "Why" and "How" in commit messages

> **💡 Tip: Install precommit hooks**
>
> Install `precommit` to automatically catch issues before committing. This helps catch spelling mistakes, formatting issues, and test failures early in your development process.
>
> * [Installation instructions](https://pre-commit.com/#install)
> * [HyperShift-specific tips](./precommit-hook-help.md)

## Creating a Pull Request
1. **For small changes** (under 200 lines): Create your change and submit a pull request directly.

2. **For larger changes** (200+ lines): Get feedback on your approach first by opening a GitHub issue or posting in the #project-hypershift Slack channel. This prevents situations where large changes get declined after significant work.

3. **Write a clear PR title**: Prefix with your Jira ticket number (e.g., "OCPBUGS-12345: Fix memory leak in controller"). See [example PR](https://github.com/openshift/hypershift/pull/2233).

4. **Open the PR in draft mode**: Use `/auto-cc` to assign reviewers to your PR in draft mode. Keep the PR in draft mode until:

    - all the required labels are on the PR
    - all required tests are passing

5. **Explain the value**: Always describe how your change improves the project in the PR description.

> **📝 Note: Release Information**
>
> This repository contains code for both the HyperShift Operator and Control Plane Operator (part of OCP payload), which may have different release cadences.

## Controller Code Review Standards

These patterns are based on common review feedback from maintainers. Following them will reduce review cycles.

### Controller Error Handling

#### Non-retryable conditions should return nil
- Missing labels or annotations → return `nil` (adding them triggers new event)
- Resource doesn't belong to this controller → return `nil`
- Expected "not found" conditions → return `nil`

**Bad:**
```go
if !hasLabel(obj, "my-label") {
    return ctrl.Result{}, fmt.Errorf("missing label")  // ❌ Causes retry loop
}
```

**Good:**
```go
if !hasLabel(obj, "my-label") {
    log.Info("missing label, waiting for update")
    return ctrl.Result{}, nil  // ✅ Will reconcile when label is added
}
```

#### Retryable errors should return error
- API call failures (Get, Update, Patch) that could succeed on retry

### Controller Filtering

#### Check namespace labels early
Before expensive operations, verify the namespace has the HCP label:
```go
ns := &corev1.Namespace{}
if err := r.Get(ctx, types.NamespacedName{Name: obj.Namespace}, ns); err != nil {
    return ctrl.Result{}, nil  // Ignore if namespace not found
}
if ns.Labels["hypershift.openshift.io/hosted-control-plane"] != "true" {
    return ctrl.Result{}, nil  // Not an HCP namespace
}
```

#### Validate references before fetching
Check that reference fields are non-nil and have expected Kind before API calls:
```go
if ref := obj.Spec.InfrastructureRef; ref == nil || ref.Kind != "AWSMachineTemplate" {
    return ctrl.Result{}, nil
}
// Now safe to fetch
```

#### Only patch when there's a difference
```go
original := obj.DeepCopy()
// ... make changes ...
if !equality.Semantic.DeepEqual(original, obj) {
    if err := r.Patch(ctx, obj, client.MergeFrom(original)); err != nil {
        return ctrl.Result{}, err
    }
}
```

### Logging

#### Use controller-runtime logger
```go
// ❌ Bad
klog.Infof("processing %s", obj.Name)

// ✅ Good
log := ctrl.LoggerFrom(ctx)
log.Info("processing", "name", obj.Name)
```

#### Use Events for user-visible state changes
Important errors/warnings should emit Events, not just logs:
```go
r.Recorder.Event(obj, corev1.EventTypeWarning, "ConfigError", "missing required annotation")
```

### Code Hygiene

#### Remove unrelated changes
- PRs should not include reformatting of unrelated code
- Avoid whitespace-only changes outside the PR scope

#### Use clear variable names
- Name should reflect purpose (e.g., `region` not `cacheID` if it's a region)
- Avoid generic names like `data`, `result`, `temp`

#### Feature flags require complete config
```go
// ❌ Bad - enables feature without required config
if opts.EnableFeatureX {
    setupFeatureX(opts.Credentials)  // Credentials might be nil!
}

// ✅ Good
if opts.EnableFeatureX && opts.Credentials != "" {
    setupFeatureX(opts.Credentials)
}
```

### Multi-tenancy Awareness

- MachineDeployments may exist for non-HyperShift clusters
- Always verify resource ownership before acting
- Check for expected annotations/labels before processing

## API Design Standards

These patterns are based on API review feedback from maintainers (enxebre, JoelSpeed, muraee, csrwng).

### Avoid bools in APIs - use enums
Bools don't evolve well over time. Use string enums with meaningful values:
```go
// ❌ Bad
SkipNodes bool `json:"skipNodes,omitempty"`

// ✅ Good
// +kubebuilder:validation:Enum=Enabled;Disabled
SkipNodes string `json:"skipNodes,omitempty"`
```

### Include units in duration field names
Don't use string durations - use integers with explicit units:
```go
// ❌ Bad - string duration is hard to validate
DelayAfterAdd string `json:"delayAfterAdd,omitempty"`

// ✅ Good - unambiguous, easy to validate
// +kubebuilder:validation:Minimum=0
// +kubebuilder:validation:Maximum=86400
DelayAfterAddSeconds int32 `json:"delayAfterAddSeconds,omitempty"`
```

### Document limits and defaults in godoc
Users can't see kubebuilder validations directly - document in comments:
```go
// DelayAfterAddSeconds specifies how long to wait after adding a node.
// It must be between 0 and 86400 (24 hours).
// When omitted, the default value of 60 is used.
// +kubebuilder:validation:Minimum=0
// +kubebuilder:validation:Maximum=86400
DelayAfterAddSeconds *int32 `json:"delayAfterAddSeconds,omitempty"`
```

### Default in code, not API
Avoid kubebuilder default markers - default in code and document in godoc:
```go
// ❌ Bad - defaults on API level
// +kubebuilder:default=60
DelaySeconds int32

// ✅ Good - default in code, documented
// When omitted, defaults to 60.
DelaySeconds *int32
```

### Set MinItems to prevent empty list issues
Unstructured clients can set empty lists that won't round-trip through structured clients:
```go
// +kubebuilder:validation:MinItems=1
// +kubebuilder:validation:MaxItems=10
Expanders []string `json:"expanders"`
```

### Mark immutable fields explicitly
Use XValidation for fields that shouldn't change after creation:
```go
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="isFlannel is immutable"
IsFlannel string `json:"isFlannel,omitempty"`
```

### Use pointers for optional fields with valid empty values
If empty string is valid AND field is required:
```go
// Required field where empty string is a valid value
ProjectID *string `json:"projectID"`
```

## Status Update Patterns

### Use Patch instead of Update
Patch is preferred for status updates to avoid conflicts:
```go
// ❌ Bad
r.cpClient.Status().Update(ctx, hcp)

// ✅ Good
r.cpClient.Status().Patch(ctx, hcp, client.MergeFrom(originalHCP))
```

### Use meta.SetStatusCondition return value
The function returns a bool indicating if the condition changed:
```go
// ❌ Bad - unnecessary DeepEqual
original := hcp.DeepCopy()
meta.SetStatusCondition(&hcp.Status.Conditions, condition)
if !equality.Semantic.DeepEqual(original.Status.Conditions, hcp.Status.Conditions) {
    // patch
}

// ✅ Good - use return value
if meta.SetStatusCondition(&hcp.Status.Conditions, condition) {
    // condition changed, patch
}
```

### Define condition constants together
Keep condition types and reasons in the same file:
```go
// In hostedcluster_conditions.go
const (
    ConditionAvailable = "Available"
    ReasonAvailableOK = "AvailableOK"
    ReasonAvailableFailed = "AvailableFailed"
)
```

## Code Organization

### Extract complex logic to helper functions
Anonymous functions in struct initialization hurt readability and testability:
```go
// ❌ Bad - anonymous function in struct
VolumeMounts: func() []corev1.VolumeMount {
    mounts := []corev1.VolumeMount{...}
    if condition {
        mounts = append(mounts, ...)
    }
    return mounts
}(),

// ✅ Good - extracted helper
VolumeMounts: buildVolumeMounts(globalPullSecretName, originalPullSecretName),
```

### Keep related constants together
Avoid spreading related definitions across files:
```go
// ✅ Good - reasons defined with conditions
// hostedcluster_conditions.go
const (
    ControlPlaneToDataPlaneConnectivity = "ControlPlaneToDataPlaneConnectivity"
    ControlPlaneToDataPlaneOKReason = "ControlPlaneToDataPlaneOK"
    ControlPlaneToDataPlaneNoPodsReason = "NoKonnectivityAgentPodsFound"
)
```

## Breaking Changes

### Immutable field changes require migration
Changing selectors on DaemonSets, StatefulSets, etc. will fail on upgrade:
```go
// ⚠️ Selector changes are immutable!
// Old: name: foo
// New: app: foo  ← This WILL FAIL on upgrade

// Solution: Delete old resource before creating new one
```

### Document behavioral changes in release notes
If merge precedence or default behavior changes, document prominently.

### Cannot add required fields to shipped APIs
Adding a required field to a released API is a breaking change:
```go
// ❌ Bad - breaks existing resources on upgrade
// +required
NewRequiredField string `json:"newRequiredField"`

// ✅ Good - optional with default in controller
// When omitted, defaults to "Multi". +optional
PayloadArchitecture *string `json:"payloadArchitecture,omitempty"`
```

## Naming and Documentation

### Use consistent naming conventions
- Use `API` not `Api` in field names: `kubeAPICustomName` not `kubeApiCustomName`
- Use lowercase for field references in godoc (serialized form): `customKubeConfig` not `CustomKubeConfig`
- Consolidate terminology - don't mix `custom`, `external`, `user` for the same concept

### Write godoc in prose
Godoc should be complete sentences, not bullet points:
```go
// ❌ Bad
// - Sets the delay
// - Must be positive

// ✅ Good
// DelayAfterAddSeconds specifies how long to wait after adding a node
// before considering it for scale down. It must be between 0 and 86400.
// When omitted, the default value of 60 is used.
```

### Document day 1 and day 2 behavior
Explain what happens both on creation and on update:
```go
// CustomKubeConfig specifies configuration to generate a kubeconfig.
// When set, the controller generates a secret with the given name.
// This field is optional on creation. When removed after creation,
// the generated secret is deleted.
```

## Condition Patterns

### Conditions should be informative, not blocking
Don't short-circuit reconciliation based on condition status:
```go
// ❌ Bad - blocks reconciliation
if !conditionTrue(np, SupportedVersionSkew) {
    return ctrl.Result{}, nil
}

// ✅ Good - continues operating, sets informative condition
meta.SetStatusCondition(&np.Status.Conditions, metav1.Condition{
    Type:    SupportedVersionSkew,
    Status:  metav1.ConditionFalse,
    Reason:  "UnsupportedSkew",
    Message: "NodePool version exceeds supported skew policy",
})
// Continue reconciliation...
```

### Use Unknown for error states, not removal
Don't remove conditions on error - set them to Unknown:
```go
// ❌ Bad
meta.RemoveStatusCondition(&conditions, ConditionType)

// ✅ Good
meta.SetStatusCondition(&conditions, metav1.Condition{
    Type:   ConditionType,
    Status: metav1.ConditionUnknown,
    Reason: "ErrorChecking",
})
```

### Set conditions consistently for all error paths
If setting conditions for some errors, set them for all:
```go
// ❌ Bad - inconsistent condition setting
if err := check1(); err != nil {
    setCondition(False, err.Error())
    return err
}
if err := check2(); err != nil {
    return err  // ← Missing condition!
}
```

### Be specific in condition documentation
Document the exact policy and what happens when condition is false:
```go
// SupportedVersionSkew signals if the NodePool version falls within
// the supported skew policy: NodePool minor version must be within
// N-2 of the control plane version. When false, the NodePool will
// continue operating but falls out of support scope.
```

## Testing

### Unit test validation functions
Especially for unmarshaled external APIs - functions should be well documented and tested.

### Tests should be deterministic
Don't add defensive nil checks for "should never happen":
```go
// ❌ Bad - defensive check in test
if nodePool == nil {
    t.Skip("nodepool not found")
}

// ✅ Good - expect deterministic result
require.NotNil(t, nodePool)
```

## Performance

### Reuse existing clients
Don't recreate clients in functions when the reconciler already has one:
```go
// ❌ Bad - creates new client every reconcile
func (r *Reconciler) doSomething() {
    client := buildClient(config)  // ← Wasteful
}

// ✅ Good - use reconciler's client
func (r *Reconciler) doSomething() {
    r.Client.Get(ctx, ...)
}
```

### Be careful with requests to guest cluster
Minimize uncached requests to guest cluster for non-watched objects.