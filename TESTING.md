# Testing Conventions

This document defines HyperShift's unit test conventions. All contributors (human and AI) must follow these rules when writing or modifying tests.

## Unit Test Creation

Every new or modified function must have accompanying unit tests. For controller reconciliation methods, test at minimum: happy path, missing/empty input, disabled capability, and the primary error path.

## Test Case Naming

Every Go test case name **must** follow this format:

```go
name: "When <condition>, it should <expected behavior>"
```

Examples:

```go
// Good
name: "When NodePool has valid image ID, it should create Azure machine template"
name: "When subnet ID is invalid, it should return error"
name: "When encryption is enabled, it should configure disk encryption set"
name: "When NodePool has no managed identity, it should create template without identity"
name: "When NodePool has ImageID, it should create basic Azure machine template"
name: "When subnet ID is invalid, it should return error with subnet parse message"

// Bad — do not use generic names
name: "nominal case without managed identity"
name: "basic Azure machine template with ImageID"
name: "error case - invalid subnet ID"
```

## Test Function Naming

Unit test functions must be named after the function they test, using the standard Go `Test<FunctionName>` convention:

```go
// Testing function ReconcileNodePool
func TestReconcileNodePool(t *testing.T) { ... }

// Testing function buildAzureMachineTemplate
func TestBuildAzureMachineTemplate(t *testing.T) { ... }
```

Do not use generic or disconnected test function names like `TestNodePoolFeatures`, `TestAzureIntegration`, or `TestVariousCases`.

Each production function or method must map to at most one top-level unit test function. If a function is complex enough to warrant many test cases, use table-driven tests or `t.Run` subtests within a single test function, with the "When...it should..." naming for each case.

Prefer `Test<ReceiverType>_<MethodName>` for method tests so methods with common names such as `Reconcile`, `Validate`, and `Run` map unambiguously:

```go
// Testing (*NodePoolReconciler).Reconcile
func TestNodePoolReconciler_Reconcile(t *testing.T) { ... }
```

For compatibility, `Test<ReceiverType><MethodName>` and `Test<MethodName>` are also accepted when they resolve to exactly one production method.

The one-to-one rule applies to ordinary unit tests. It does not apply to `TestMain`, benchmarks, fuzz tests, examples, generated files, integration/e2e/envtest/request-serving entry points, or package-level behavioral tests that do not correspond to one production function. For an intentional exception that still exercises a production function, add a declaration-level suppression with a specific reason:

```go
//nolint:hypershiftlinter // testfuncstructure: validates compatibility across multiple functions
func TestSerializationCompatibility(t *testing.T) { ... }
```

## Test Placement

- Place unit tests next to the code they test — in the same package directory, in a `_test.go` file.
- Do **not** place new tests in `test/integration/` — this is a legacy directory. New unit tests go alongside the code under test. New integration-style tests that require a cluster should use `test/e2e/` or `test/envtest/`.

## Additional Rules

- Do not export functions that are only used in tests. Use unexported helpers or keep them in `_test.go` files.
- Use real-world values in test fixtures when possible (e.g., `quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64` instead of `example.com/image:latest`). Real values catch edge cases that synthetic values miss.
- When adding test assertions for OwnerReferences, check that they were actually set during reconciliation — not just that the object exists.
