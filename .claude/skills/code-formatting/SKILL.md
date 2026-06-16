---
name: Code Formatting
description: "MANDATORY: When writing Go tests, you MUST use 'When...it should...' format for ALL test names. When writing any Go code, you MUST remind user to run 'make lint-fix' and 'make verify'. These are non-negotiable HyperShift requirements."
---

# Code Quality, Formatting and Conventions

## Code Formatting and Linting

- **Use `make lint-fix`** after writing Go code to automatically fix most linting issues
- **Run `make verify`** to verify both linting and tests pass before committing
- **For markdown files**, use `make verify-codespell` to catch spelling errors

## Test Conventions - MANDATORY

### Test naming

**NON-NEGOTIABLE RULE**: Every Go test name MUST follow this exact format:
```go
name: "When <condition>, it should <expected behavior>"
```

Examples:
```go
name: "When NodePool has valid image ID, it should create Azure machine template"
name: "When subnet ID is invalid, it should return error"
name: "When encryption is enabled, it should configure disk encryption set"
```

**NEVER use generic names like:**
- ❌ "nominal case without managed identity"
- ❌ "basic Azure machine template with ImageID"
- ❌ "error case - invalid subnet ID"

**ALWAYS use "When...it should..." format:**
- ✅ "When NodePool has no managed identity, it should create template without identity"
- ✅ "When NodePool has ImageID, it should create basic Azure machine template"
- ✅ "When subnet ID is invalid, it should return error with subnet parse message"

### Unit test function naming

Unit test functions MUST be named after the function they test, using the standard Go `Test<FunctionName>` convention:

```go
// Testing function ReconcileNodePool
func TestReconcileNodePool(t *testing.T) { ... }

// Testing function buildAzureMachineTemplate
func TestBuildAzureMachineTemplate(t *testing.T) { ... }
```

**NEVER use generic or disconnected test function names like:**
- ❌ `TestNodePoolFeatures`
- ❌ `TestAzureIntegration`
- ❌ `TestVariousCases`

Each test function should map 1:1 to the function under test. If a function is complex enough to warrant many test cases, use table-driven tests within a single `Test<FunctionName>` function with the "When...it should..." naming for each case.

### Unit test placement

- **Always include unit tests** when creating new functions or modifying existing ones
- **Place unit tests next to the code they test** — in the same package directory, in a `_test.go` file
- **Do NOT place new tests in `test/integration/`** — this is a legacy directory. New unit tests go alongside the code under test. New integration-style tests that require a cluster should use `test/e2e/` or `test/envtest/`

## Quick Checklist

Before committing:
- [ ] Ran `make lint-fix` on Go code
- [ ] Ran `make verify` (passes)
- [ ] Tests use "When...it should..." naming format
- [ ] New functions have unit tests
- [ ] Markdown checked with `make verify-codespell`
