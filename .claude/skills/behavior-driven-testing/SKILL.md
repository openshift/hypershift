---
name: behavior-driven-testing
description: "Guides writing of behavior-driven Go unit tests that prioritize testing meaningful behaviors over chasing code coverage. Triggers when writing, reviewing, or planning unit tests -- 'add tests', 'write tests', 'test this function', 'improve coverage', or implementing features/bugfixes that need tests."
paths: "**/*_test.go, **/tests/**/*.yaml, test/envtest/**"
---

# Behavior-Driven Unit Testing in Go

This skill ensures unit tests verify meaningful behaviors rather than just exercising code paths. Coverage is a useful signal, but a test suite full of shallow assertions that happen to touch every line is worse than a smaller suite that deeply validates the behaviors users and callers actually depend on.

## The Core Question

Before writing any test case, ask: **"What behavior would break if this code were wrong?"**

A behavior is something a caller or user would notice:
- A function returns the wrong value for a given input
- A side effect (resource created, annotation set, error returned) doesn't happen when it should
- A side effect happens when it shouldn't
- An invariant is violated (e.g., a service that must always be LoadBalancer type becomes ClusterIP)

If you can't articulate what would break, you probably don't need that test case. Conversely, if you can articulate a behavior that no existing test covers, that's a gap worth filling regardless of what the coverage number says.

## Test Structure

Use table-driven tests with Go subtests. This is idiomatic Go and the standard pattern in this codebase.

```go
func TestReconcileWidget(t *testing.T) {
    tests := []struct {
        name     string
        // inputs that vary per case
        widget   Widget
        options  WidgetOptions
        // expected behavioral outcomes
        wantErr  bool
        wantType WidgetType
    }{
        {
            name:     "When widget has valid config, it should reconcile successfully",
            widget:   validWidget(),
            options:  defaultOptions(),
            wantErr:  false,
            wantType: WidgetTypeActive,
        },
        {
            name:    "When widget config is missing required field, it should return a validation error",
            widget:  widgetMissingName(),
            options: defaultOptions(),
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            g := NewWithT(t)

            result, err := ReconcileWidget(tt.widget, tt.options)
            if tt.wantErr {
                g.Expect(err).To(HaveOccurred())
                return
            }
            g.Expect(err).ToNot(HaveOccurred())
            g.Expect(result.Type).To(Equal(tt.wantType))
        })
    }
}
```

### Naming Convention

Every test case name MUST use Gherkin-style format:

```
"When <precondition/scenario>, it should <expected observable behavior>"
```

The name should read like a specification. Someone unfamiliar with the code should understand what the test validates just from reading the name.

Good names describe the scenario and its behavioral consequence:
- `"When NodePool has 3 available replicas, it should report 3 available nodes"`
- `"When encryption is enabled but key is missing, it should return an error"`
- `"When the cluster is private, it should set the internal load balancer annotation"`

Bad names describe implementation or are vague:
- `"test encryption"` -- what about encryption?
- `"error case"` -- which error? why?
- `"with 3 replicas"` -- what should happen?
- `"nominal"` -- meaningless to a reader

For tests where the precondition involves a "Given" context that differs from the "When" trigger, you can extend the pattern: `"Given <context>, when <action>, it should <behavior>"`. Use this sparingly -- most tests are fine with just "When...it should...".

### Map-Based Table Tests

For simpler cases where the test name doubles as the map key, you can use a map instead of a slice:

```go
tests := map[string]struct {
    input    string
    expected string
}{
    "When input is valid CIDR, it should return first usable IP": {
        input:    "192.168.1.0/24",
        expected: "192.168.1.1",
    },
}

for name, tt := range tests {
    t.Run(name, func(t *testing.T) {
        // ...
    })
}
```

## Assertions with Gomega

Use gomega for all assertions. Import it with the dot-import pattern:

```go
import (
    "testing"

    . "github.com/onsi/gomega"
)
```

Initialize gomega per subtest using `NewWithT(t)` (preferred) or `NewGomegaWithT(t)`:

```go
t.Run(tt.name, func(t *testing.T) {
    g := NewWithT(t)

    g.Expect(err).ToNot(HaveOccurred())
    g.Expect(result.Name).To(Equal("expected-name"))
    g.Expect(svc.Annotations).To(HaveKeyWithValue("key", "value"))
    g.Expect(list).To(HaveLen(3))
    g.Expect(count).To(BeNumerically(">=", 1))
    g.Expect(svc.Annotations).ToNot(HaveKey("removed-annotation"))
})
```

### Choosing the Right Matcher

Pick matchers that express the behavior you're checking, not just that something is non-nil:

| Checking | Use | Avoid |
|----------|-----|-------|
| Error occurred | `g.Expect(err).To(HaveOccurred())` | `g.Expect(err).ToNot(BeNil())` |
| No error | `g.Expect(err).ToNot(HaveOccurred())` | `g.Expect(err).To(BeNil())` |
| Error message content | `g.Expect(err).To(MatchError(ContainSubstring("...")))` | String comparison on `err.Error()` |
| Map has key+value | `g.Expect(m).To(HaveKeyWithValue(k, v))` | Indexing into map then comparing |
| Slice length | `g.Expect(s).To(HaveLen(n))` | `g.Expect(len(s)).To(Equal(n))` |
| Numeric comparison | `g.Expect(x).To(BeNumerically(">=", y))` | Manual comparison |
| Slice contains element | `g.Expect(s).To(ContainElement(e))` | Looping and comparing |
| Empty collection | `g.Expect(s).To(BeEmpty())` | `g.Expect(len(s)).To(Equal(0))` |

## Choosing What to Test

### Think in Behaviors, Not Lines

When deciding what test cases to write, think about the function's behavioral contract:

1. **Happy paths**: What does the function do when everything is valid? Test the primary use cases that callers depend on.

2. **Boundary conditions**: What happens at the edges? Empty inputs, zero values, maximum lengths, nil pointers. These are where bugs hide.

3. **Error paths that callers handle**: If the function returns an error that callers react to (retry, degrade, propagate), test that the error occurs and carries useful information.

4. **State transitions**: If the function changes state (sets annotations, updates status, creates resources), verify the state after the call reflects the documented behavior.

5. **Invariants**: Things that must always be true regardless of input. For example, "the service type is always LoadBalancer" or "the owner reference is always set".

### What NOT to Test

- **Internal implementation details**: Don't assert on the order of internal function calls, private field values, or intermediate state that callers never observe. These tests break on refactors without catching bugs.

- **Trivial getters/setters**: A function that just returns a field doesn't need its own test.

- **Framework behavior**: Don't test that `controller-runtime` calls your reconciler or that Kubernetes applies owner references correctly. Trust the framework; test your logic.

- **Every permutation**: If a function takes 3 booleans, you don't need 8 test cases. Identify which combinations represent meaningfully different behaviors and test those.

## Test Organization

### Parallel Execution

Use `t.Parallel()` for tests that don't share mutable state:

```go
func TestValidation(t *testing.T) {
    t.Parallel()

    tests := []struct { /* ... */ }{
        // ...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            // ...
        })
    }
}
```

Don't use `t.Parallel()` when tests modify shared state, use `t.Setenv()`, or interact with a shared fake client.

### Helper Functions

Extract test object construction into helper functions when the same setup appears across multiple test functions. Mark them with `t.Helper()` so failures report the caller's line:

```go
func newTestHostedCluster(platform hyperv1.PlatformType) *hyperv1.HostedCluster {
    return &hyperv1.HostedCluster{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-cluster",
            Namespace: "clusters",
        },
        Spec: hyperv1.HostedClusterSpec{
            Platform: hyperv1.PlatformSpec{Type: platform},
        },
    }
}
```

Only create helpers when they eliminate real duplication across tests. Three lines of inline setup is better than a helper used once.

### Fake Clients for Controller Tests

When testing code that interacts with the Kubernetes API, use `controller-runtime`'s fake client:

```go
scheme := runtime.NewScheme()
hyperv1.AddToScheme(scheme)
corev1.AddToScheme(scheme)

fakeClient := fake.NewClientBuilder().
    WithScheme(scheme).
    WithObjects(existingResources...).
    Build()

// Use t.Context() for operations that need a context
result := &hyperv1.HostedCluster{}
err := fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hc), result)
```

### Mocking External Dependencies

For external services (cloud APIs, HTTP clients), define interfaces and provide test implementations:

```go
type EC2Client interface {
    DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error)
}

type mockEC2Client struct {
    describeFunc func(ctx context.Context, input *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error)
}

func (m *mockEC2Client) DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
    return m.describeFunc(ctx, input)
}
```

This lets each test case define exactly the behavior it needs from the dependency, keeping the focus on the code under test.

## Context in Tests: Use `t.Context()`

Since Go 1.24+, `testing.T` provides a `Context()` method that returns a context automatically cancelled when the test ends. Prefer this over manually creating contexts:

```go
// Preferred
result, err := reconciler.Reconcile(t.Context(), req)

// Avoid
result, err := reconciler.Reconcile(context.TODO(), req)
result, err := reconciler.Reconcile(context.Background(), req)
```

`t.Context()` is better because:
- It's automatically cancelled when the test finishes, preventing goroutine leaks
- It makes test cleanup implicit rather than requiring manual `cancel()` calls
- It signals intent: this context is scoped to this test's lifetime

Use `context.Background()` only when you genuinely need a context that outlives the test (rare) or in `TestMain`.

## Envtests for API Changes

API changes (anything under `api/`) must include envtest coverage. Envtests are the unit tests for the API -- they validate that CRD schemas, validation rules, defaulting, and ratcheting behavior work correctly against a real Kubernetes API server.

Envtest suites live in YAML files under `cmd/install/assets/hypershift-operator/tests/` and follow the openshift/api test convention. The framework is in `test/envtest/`.

### When to Write Envtests

Add envtest coverage when you:
- Add or modify CRD validation rules (CEL expressions, enum constraints, required fields)
- Add defaulting logic to a CRD field
- Change field immutability constraints
- Add new API fields that have validation requirements
- Modify ratcheting behavior (allowing previously-invalid values to persist through updates)

### Envtest Suite Structure

Each test suite YAML targets a specific CRD and contains `onCreate` and/or `onUpdate` test cases:

```yaml
apiVersion: apiextensions.k8s.io/v1
name: "HostedCluster validation description"
crdName: hostedclusters.hypershift.openshift.io
version: v1beta1
tests:
  onCreate:
  - name: When clusterID is not RFC4122 UUID it should fail
    initial: |
      apiVersion: hypershift.openshift.io/v1beta1
      kind: HostedCluster
      spec:
        clusterID: "foo"
        # ... minimal valid spec with the invalid field
    expectedError: "clusterID must be an RFC4122 UUID value"

  - name: When clusterID is valid UUID it should pass
    initial: |
      apiVersion: hypershift.openshift.io/v1beta1
      kind: HostedCluster
      spec:
        clusterID: "123e4567-e89b-12d3-a456-426614174000"
        # ... minimal valid spec

  onUpdate:
  - name: When immutable field is changed it should fail
    initial: |
      apiVersion: hypershift.openshift.io/v1beta1
      kind: HostedCluster
      spec:
        infraID: "original-id"
        # ... minimal valid spec
    updated: |
      apiVersion: hypershift.openshift.io/v1beta1
      kind: HostedCluster
      spec:
        infraID: "changed-id"
        # ... minimal valid spec
    expectedError: "infraID is immutable"
```

### Key Principles for Envtests

- **Test names use the same Gherkin format**: `"When <condition> it should <behavior>"`
- **Minimal YAML**: Include only the fields needed for the test, plus required fields for a valid resource. Don't duplicate the entire spec when testing one field.
- **Test both positive and negative cases**: For each validation rule, test that invalid input is rejected AND that valid input is accepted.
- **Group related tests**: Each YAML file should focus on a logical area (validation, networking, services, platform-specific).
- **`onCreate` tests**: Verify validation on initial resource creation.
- **`onUpdate` tests**: Verify immutability, ratcheting, and update-specific validation. Use `initialCRDPatches` for testing ratcheting behavior where the CRD schema itself changes between the initial and updated versions.
- **Run envtests**: `make test-envtest` runs the envtest suite (requires `setup-envtest` binaries).

## Coverage as a Compass, Not a Target

Aim for high coverage, but interpret it as a signal:

- **Low coverage on a complex function** = likely missing important behavior tests. Investigate which branches represent real user-facing behaviors.
- **100% coverage with weak assertions** = false confidence. A test that calls a function and only checks `err == nil` covers the line but doesn't verify the behavior.
- **Uncovered error-handling code** = often acceptable if the error is unreachable in practice (e.g., marshaling a known-good struct). Don't write tests for impossible paths just to bump a number.

When you see an uncovered line, ask: "If this line had a bug, would a user or caller notice?" If yes, write a test. If no, move on.

## Reviewing Existing Tests

When reviewing tests (your own or others'), check:

1. **Does the test name explain the behavior?** Can you understand what it validates without reading the body?
2. **Does it test behavior or implementation?** Would a refactor that preserves behavior break this test?
3. **Are the assertions meaningful?** Do they check the behavioral outcome, or just that the function ran without panicking?
4. **Are edge cases covered?** Not every edge case, but the ones that represent real risks.
5. **Is it readable?** A test is documentation. Someone should be able to read it and understand the function's contract.
