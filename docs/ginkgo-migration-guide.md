# Ginkgo/Gomega Test Migration Guide

## Quick Reference

**Migration Phase**: Phase 2 - File-by-file migration in progress
**Framework**: Ginkgo v2.23.4 + Gomega
**Package**: `test/e2e` (single package)

**Migration Agent**: Use `ginkgo-migration-sme` agent (see `.claude/agents/ginkgo-migration-sme.md`)
**Quick Command**: "Claude, migrate [filename] to Ginkgo"

**Critical Constraints**:
- ⚠️ Parallelization is OFF during migration (mixing frameworks causes failures)
- ⚠️ Migrate ONE file at a time
- ⚠️ Test after each migration

**Files Migrated**: 0 / 27
**Current Status**: All files pending migration (see progress table in [Section 2.1](#21-current-migration-status))

**Decision Flow**:
```text
Working with E2E tests?
├─ Modifying existing test?
│  ├─ File migrated to Ginkgo? → Use Ginkgo patterns (Section 4)
│  └─ File NOT migrated? → Use native Go testing (temporary)
├─ Adding new test?
│  └─ Add to first unmigrated file OR create as Ginkgo if all migrated
└─ Migrating test file?
   └─ Use ginkgo-migration-sme agent (Section 3.1) OR manual workflow (Section 3.2)
```

---

## 1. Introduction

### 1.1 Overview

HyperShift is migrating e2e tests (located in `test/e2e/`) from native Go testing (`testing.T`) to Ginkgo v2 + Gomega for improved test organization, better assertions, and BDD-style test descriptions.

**Scope**: This migration applies **only to E2E tests** in the `test/e2e/` directory.

**Current state**: E2E tests use `testing.T` with Gomega via `NewWithT(t)`
**Target state**: E2E tests use Ginkgo test suites with native Gomega integration

### 1.2 Why Ginkgo for E2E Tests?

- **Better organization & readability**: Nested `Describe`/`Context`/`It` blocks with descriptive test names for complex cluster lifecycle scenarios
- **Powerful setup/teardown**: `BeforeEach`, `AfterEach`, `BeforeSuite`, `AfterSuite` for clean cluster setup/cleanup
- **Async operations**: Built-in `Eventually`/`Consistently` for long-running E2E operations
- **Enhanced testing**: Table-driven tests, parallel execution, focus/skip capabilities, and native Gomega integration (no `NewWithT(t)` wrapper needed)

### 1.3 Scope

The HyperShift E2E tests are in a **single package**: `test/e2e`. The migration proceeds **file by file**, with files listed alphabetically in the progress table.

**File Eligibility Criteria** - Files are eligible for migration if they meet **ALL** of the following:
- Located in the `test/e2e/` directory
- Filename ends with `_test.go`
- Contains the `//go:build e2e` build tag
- Contains at least one `func Test*` function to migrate

---

## 2. Migration Status & Planning

### 2.1 Current Migration Status

**Phase 1: Framework Setup** ✅ COMPLETE
- [x] Added Ginkgo v2.23.4 dependency to go.mod
- [x] Created migration documentation (this file)
- [x] Created suite file for test/e2e package

**Phase 2: test/e2e Package Migration** ⏳ IN PROGRESS
- [x] Suite file created (`test/e2e/e2e_suite_test.go`)
- [ ] Migrate test files (see progress table below)

#### Migration Progress Table

| File | Status | Date | Notes |
|------|--------|------|-------|
| autoscaling_test.go | ⏳ Pending | - | - |
| azure_scheduler_test.go | ⏳ Pending | - | - |
| chaos_test.go | ⏳ Pending | - | - |
| control_plane_upgrade_test.go | ⏳ Pending | - | - |
| create_cluster_test.go | ⏳ Pending | - | - |
| external_oidc_test.go | ⏳ Pending | - | - |
| karpenter_test.go | ⏳ Pending | - | - |
| nodepool_additionalTrustBundlePropagation_test.go | ⏳ Pending | - | - |
| nodepool_arm64_create_test.go | ⏳ Pending | - | - |
| nodepool_autorepair_test.go | ⏳ Pending | - | - |
| nodepool_day2_tags_test.go | ⏳ Pending | - | - |
| nodepool_kms_root_volume_test.go | ⏳ Pending | - | - |
| nodepool_kv_advanced_multinet_test.go | ⏳ Pending | - | - |
| nodepool_kv_cache_image_test.go | ⏳ Pending | - | - |
| nodepool_kv_jsonpatch_test.go | ⏳ Pending | - | - |
| nodepool_kv_multinet_test.go | ⏳ Pending | - | - |
| nodepool_kv_nodeselector_test.go | ⏳ Pending | - | - |
| nodepool_kv_qos_guaranteed_test.go | ⏳ Pending | - | - |
| nodepool_machineconfig_test.go | ⏳ Pending | - | - |
| nodepool_mirrorconfigs_test.go | ⏳ Pending | - | - |
| nodepool_nto_machineconfig_test.go | ⏳ Pending | - | - |
| nodepool_nto_performanceprofile_test.go | ⏳ Pending | - | - |
| nodepool_osp_advanced_test.go | ⏳ Pending | - | - |
| nodepool_prev_release_test.go | ⏳ Pending | - | - |
| nodepool_rolling_upgrade_test.go | ⏳ Pending | - | - |
| nodepool_test.go | ⏳ Pending | - | - |
| nodepool_upgrade_test.go | ⏳ Pending | - | - |
| olm_test.go | ⏳ Pending | - | - |
| upgrade_hypershift_operator_test.go | ⏳ Pending | - | - |

#### Maintaining the Progress Table

**IMPORTANT for all agents working with E2E tests during migration**:

When performing E2E test migration or modifying E2E test files:
1. **After migrating a test file**: Update the progress table to mark the file as completed with date and any relevant notes
2. **If adding a new E2E test file**: Check if it meets the eligibility criteria (see Section 1.3). If it does, add it to the progress table in alphabetical order
3. **If renaming an E2E test file**: Update the filename in the progress table accordingly
4. **If deleting an E2E test file**: Remove it from the progress table

**Table Update Format**:
- **Status**: Change `⏳ Pending` to `✅ Migrated`
- **Date**: Use `YYYY-MM-DD` format (e.g., `2025-10-25`)
- **Notes**: Include test count if different, any issues encountered, or "-" if none

**Example**:

| File | Status | Date | Notes |
|------|--------|------|-------|
| nodepool_test.go | ✅ Migrated | 2025-10-25 | 5 tests migrated successfully |
| olm_test.go | ✅ Migrated | 2025-10-26 | - |

### 2.2 Migration Constraints

**IMPORTANT**: During the migration period, test parallelization must be turned off.

**Why**: Mixing native Go tests (`testing.T`) with Ginkgo tests in the same package while using Go's built-in `-test.parallel` flag causes test failures and unpredictable behavior. The two testing frameworks handle parallelization differently and are incompatible when run together.

**Current workaround**: The script `hack/ci-test-e2e.sh` (lines 25-36) includes temporary logic to filter out `-test.parallel` flags passed from the CI environment:

```bash
# Filter out incompatible parallel flags for ginkgo
filtered_args=()
for arg in "$@"; do
  case "$arg" in
    -test.parallel=*|--test.parallel=*)
      # Skip parallel flags
      ;;
    *)
      filtered_args+=("$arg")
      ;;
  esac
done
```

**Next steps**:
1. Update openshift/release workflows to stop passing `-test.parallel` flags to HyperShift E2E tests
2. Once those changes are merged, remove the filtering logic from `hack/ci-test-e2e.sh` (lines 25-36)

**After migration**: Once all E2E tests are migrated to Ginkgo, we can use Ginkgo's native parallelization features, which are more powerful and designed for the framework. See [Section 6](#6-post-migration) for details.

### 2.3 Migration Plan

The migration proceeds **file by file** through all test files in the `test/e2e/` package, listed alphabetically in the progress table above.

The suite file (`test/e2e/e2e_suite_test.go`) has already been created and serves as the Ginkgo test suite entry point.

---

## 3. How to Migrate

### 3.1 Using the Migration Agent (Recommended)

For automated migration, use the `ginkgo-migration-sme` agent. Simply ask Claude to "migrate [filename] to Ginkgo" or "continue the E2E Ginkgo migration". The agent works file-by-file with fresh context for efficiency.

See `.claude/agents/ginkgo-migration-sme.md` for details.

### 3.2 Manual Migration (If Needed)

If you need to migrate test files manually:

1. **Suite file already exists**: `test/e2e/e2e_suite_test.go`
2. **Convert one test file** at a time using migration patterns in [Section 4](#4-migration-patterns)
3. **Run tests** after each file: `go test ./test/e2e`
4. **Fix any issues** before moving to next file
5. **Update migration tracking** in the progress table in [Section 2.1](#migration-progress-table)

### 3.3 Running and Testing

#### Standard Workflow
```bash
make e2e                 # Run all E2E tests (recommended)
go test ./test/e2e       # Direct execution
go test -v ./test/e2e    # Verbose output
```

#### Running with Race Detector
```bash
go test -race ./test/e2e    # Detect race conditions
```

**Important**: Always run with race detector after migration to catch concurrency issues.

#### Using Ginkgo CLI

For better test output and debugging during migration, use the Ginkgo CLI directly (run from repository root):

```bash
# Verbose output - shows test progress in real-time
go run github.com/onsi/ginkgo/v2/ginkgo -v ./test/e2e

# Focus on specific test by name pattern
go run github.com/onsi/ginkgo/v2/ginkgo --focus="TestNodePool" ./test/e2e

# Run specific file only
go run github.com/onsi/ginkgo/v2/ginkgo ./test/e2e/nodepool_test.go

# Fail fast - stop on first failure
go run github.com/onsi/ginkgo/v2/ginkgo --fail-fast ./test/e2e
```

**Benefits**:
- Uses the Ginkgo version from `go.mod` (no separate installation needed)
- Better test output formatting than `go test`
- More control over test execution during development

**Note**: Standard `go test ./test/e2e` and `make e2e` continue to work throughout migration.

---

## 4. Migration Patterns

### 4.1 Pattern Quick Reference

| Pattern | Use When | Complexity | Section |
|---------|----------|------------|---------|
| Simple test migration | Single test function, basic assertions | ⭐ Basic | 4.2.1 |
| Setup/teardown | Need BeforeEach/AfterEach hooks | ⭐ Basic | 4.2.2 |
| Subtests/Context | Grouping related test cases | ⭐⭐ Intermediate | 4.3.1 |
| Table-driven tests | Multiple test cases with same logic | ⭐⭐ Intermediate | 4.3.2 |
| Focused/Pending tests | Development & debugging | ⭐⭐⭐ Advanced | 4.4.1 |
| Parallel tests | Independent tests that can run concurrently | ⭐⭐⭐ Advanced | 4.4.2 |
| Suite-level setup | Expensive one-time setup | ⭐⭐⭐ Advanced | 4.4.3 |
| Async assertions | Eventually/Consistently patterns | ⭐⭐⭐ Advanced | 4.4.4 |

### 4.2 Basic Patterns

#### 4.2.1 Simple Test Migration

**Before**:
```go
func TestFoo(t *testing.T) {
    g := NewWithT(t)
    result := doSomething()
    g.Expect(result).To(Equal("expected"))
}
```

**After**:
```go
var _ = Describe("Foo", func() {
    It("should do something", func() {
        result := doSomething()
        Expect(result).To(Equal("expected"))
    })
})
```

**Key changes**:
- Remove `func Test*` wrapper
- Replace with `Describe` block (describes component/feature)
- Use `It` for individual test cases (describes behavior)
- Remove `g := NewWithT(t)` - use `Expect` directly
- Use descriptive names: "should do X", "returns Y when Z"

#### 4.2.2 Setup/Teardown

**Before**:
```go
func TestWithSetup(t *testing.T) {
    g := NewWithT(t)

    // Setup
    client := setupClient()
    defer cleanup(client)

    // Test
    result := client.DoSomething()
    g.Expect(result).NotTo(BeNil())
}
```

**After**:
```go
var _ = Describe("WithSetup", func() {
    var client *Client

    BeforeEach(func() {
        client = setupClient()
    })

    AfterEach(func() {
        cleanup(client)
    })

    It("should do something", func() {
        result := client.DoSomething()
        Expect(result).NotTo(BeNil())
    })
})
```

**Key changes**:
- Declare shared variables at `Describe` level
- Use `BeforeEach` for setup
- Use `AfterEach` for cleanup (runs even if test fails)
- Variables are fresh for each test

### 4.3 Intermediate Patterns

#### 4.3.1 Subtests/Context

**Before**:
```go
func TestMultipleCases(t *testing.T) {
    g := NewWithT(t)

    t.Run("case 1", func(t *testing.T) {
        g := NewWithT(t)
        g.Expect(doA()).To(Equal("a"))
    })

    t.Run("case 2", func(t *testing.T) {
        g := NewWithT(t)
        g.Expect(doB()).To(Equal("b"))
    })
}
```

**After**:
```go
var _ = Describe("MultipleCases", func() {
    Context("case 1", func() {
        It("should return 'a'", func() {
            Expect(doA()).To(Equal("a"))
        })
    })

    Context("case 2", func() {
        It("should return 'b'", func() {
            Expect(doB()).To(Equal("b"))
        })
    })
})
```

**Key changes**:
- Use `Context` for grouping related tests
- Each `Context` can have its own `BeforeEach`/`AfterEach`
- Flatten nested `Describe` blocks when appropriate

#### 4.3.2 Table-Driven Tests

**Before**:
```go
func TestValidation(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected error
    }{
        {"valid input", "valid", nil},
        {"invalid input", "invalid", ErrInvalid},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            g := NewWithT(t)
            err := validate(tt.input)
            g.Expect(err).To(Equal(tt.expected))
        })
    }
}
```

**After**:
```go
var _ = DescribeTable("Validation",
    func(input string, expectedErr error) {
        err := validate(input)
        Expect(err).To(Equal(expectedErr))
    },
    Entry("valid input", "valid", nil),
    Entry("invalid input", "invalid", ErrInvalid),
)
```

**Key changes**:
- Use `DescribeTable` with test function
- Each `Entry` is a row in the table
- First argument to `Entry` is the description
- Remaining arguments map to test function parameters
- Much more concise than loop-based approach

### 4.4 Advanced Patterns

#### 4.4.1 Focused/Pending Tests

**Focus on specific tests** (useful during development):
```go
FDescribe("Focused feature", func() {  // Only this runs
    It("focused test", func() { ... })
})

var _ = Describe("Normal feature", func() {
    FIt("focused test", func() { ... })  // Only this runs
    It("normal test", func() { ... })   // Skipped
})
```

**Mark tests as pending**:
```go
var _ = Describe("Feature", func() {
    PIt("pending test - not yet implemented", func() {
        // Test will be marked as pending
    })

    XIt("explicitly skipped test", func() {
        // Test will be skipped
    })
})
```

**⚠️ Note**: Don't commit focused tests! They'll fail CI.

#### 4.4.2 Parallel Tests

```go
var _ = Describe("ParallelFeature", func() {
    BeforeEach(func() {
        // Runs in parallel for each test
    })

    It("test 1", func() { ... }, OncePerOrdered)
    It("test 2", func() { ... }, OncePerOrdered)
}, Ordered)  // Tests run in order, but the whole suite can run parallel to others
```

Or run all tests in a `Describe` in parallel:
```go
var _ = Describe("ParallelFeature", func() {
    // All tests run in parallel
}, ParallelizeOnce)
```

#### 4.4.3 Suite-Level Setup

For expensive setup that should run once per suite:

```go
var _ = BeforeSuite(func() {
    // Runs once before all tests
    initializeDatabase()
    startTestServer()
})

var _ = AfterSuite(func() {
    // Runs once after all tests
    cleanupDatabase()
    stopTestServer()
})
```

#### 4.4.4 Async Assertions

Ginkgo has built-in support for async assertions:

```go
It("eventually succeeds", func() {
    Eventually(func() error {
        return checkStatus()
    }).WithTimeout(10 * time.Second).
       WithPolling(100 * time.Millisecond).
       Should(Succeed())
})

It("consistently maintains state", func() {
    Consistently(func() bool {
        return isHealthy()
    }).WithTimeout(5 * time.Second).
       Should(BeTrue())
})
```

---

## 5. Best Practices

### 5.1 Anti-Patterns to Avoid

- ❌ **Declaring shared variables inside `It()` blocks** - They won't be shared. Declare at `Describe` level, initialize in `BeforeEach`.
- ❌ **Committing focused tests** - Remove all `FIt`, `FDescribe`, `FContext` before committing (they fail CI).
- ❌ **Batch migrating without testing** - Migrate and test one file at a time to catch issues early.
- ❌ **Forgetting cleanup** - Use `AfterEach` for cleanup that must run even when tests fail.

### 5.2 Common Pitfalls

#### Variable Scope
```go
// ❌ WRONG - variables declared in It() blocks don't share state
var _ = Describe("Bad", func() {
    It("sets variable", func() {
        var client *Client  // Local to this test only!
        client = newClient()
    })
    It("uses variable", func() {
        client.DoSomething()  // ERROR: undefined variable
    })
})

// ✅ CORRECT - shared variables at Describe level
var _ = Describe("Good", func() {
    var client *Client  // Shared across tests

    BeforeEach(func() {
        client = newClient()  // Fresh for each test
    })

    It("sets up client", func() {
        Expect(client).NotTo(BeNil())
    })
    It("uses client", func() {
        client.DoSomething()
        Expect(client.Status).To(Equal("success"))
    })
})
```

#### Context Cancellation
```go
// If using context.Context, ensure proper cleanup
var _ = Describe("WithContext", func() {
    var ctx context.Context
    var cancel context.CancelFunc

    BeforeEach(func() {
        ctx, cancel = context.WithCancel(context.Background())
    })

    AfterEach(func() {
        cancel()  // Always cancel to prevent leaks
    })

    It("does something", func() {
        // Use ctx in test
    })
})
```

### 5.3 Coverage Verification

After migrating a file, verify test coverage equivalence:

**Count original tests**:
```bash
grep -c "^func Test" <original_file>.go
```

**Count migrated tests**:
- Each `It()` block = 1 test
- Each `Entry()` in `DescribeTable` = 1 test
- Subtests using `Context` should have `It()` blocks inside

**Rule**: Migrated test count must **equal or exceed** original count.

**Example**:
- Original: `func TestNodePool` with 3 `t.Run()` subtests = 3 tests
- Migrated: 1 `Describe("NodePool")` with 3 `It()` blocks = 3 tests ✅

---

## 6. Post-Migration

### 6.1 Cleanup Checklist

Complete these tasks once ALL E2E test files have been migrated to Ginkgo:

#### ✅ Task 1: Remove Parallel Flag Filtering

- [ ] Update openshift/release workflows to stop passing `-test.parallel` flags
- [ ] Remove filtering logic from `hack/ci-test-e2e.sh` (lines 25-36)
- [ ] Verify CI passes without the workaround

**Why**: Ginkgo handles parallelization natively; the filtering was only needed during mixed framework period.

**Files to modify**: `hack/ci-test-e2e.sh`, openshift/release workflow configurations

#### ✅ Task 2: Switch to Ginkgo CLI

- [ ] Update `hack/ci-test-e2e.sh` to use Ginkgo CLI instead of compiled binary
- [ ] Update `Dockerfile.e2e` if needed
- [ ] Update `Makefile` targets if needed

**Current approach**: Compile `bin/test-e2e` binary and execute it

**Target approach**: Use `go run github.com/onsi/ginkgo/v2/ginkgo` to run tests directly

Update `hack/ci-test-e2e.sh` to invoke Ginkgo similar to the [recommended CI configuration](https://onsi.github.io/ginkgo/#recommended-continuous-integration-configuration):

```bash
go run github.com/onsi/ginkgo/v2/ginkgo -r --randomize-all --randomize-suites \
  --fail-on-pending --keep-going --cover --coverprofile=coverage.txt \
  --race --trace --junit-report=junit.xml ./test/e2e
```

**Benefits**: Eliminates the need to compile the E2E test binary as a separate build artifact.

**Files to modify**: `hack/ci-test-e2e.sh`, `Dockerfile.e2e`, `Makefile`

#### ✅ Task 3: Use Native JUnit Output

- [ ] Remove `go tool test2json` conversion
- [ ] Remove `gotestsum` JUnit generation
- [ ] Remove `sed` post-processing to set suite name
- [ ] Configure Ginkgo's `--junit-report` flag
- [ ] Set suite name in Ginkgo reporter configuration if needed

**Why**: Ginkgo can generate JUnit XML reports directly via the `--junit-report` flag, eliminating the need for a conversion toolchain.

**Files to modify**: `hack/ci-test-e2e.sh`

#### ✅ Task 4: Migrate TestMain Setup/Teardown

- [ ] Review the `TestMain` function in E2E tests
- [ ] Move setup logic to `BeforeSuite` in `test/e2e/e2e_suite_test.go`
- [ ] Move teardown logic to `AfterSuite` in `test/e2e/e2e_suite_test.go`
- [ ] Remove or simplify `TestMain` function

**Before migration**: Setup/teardown in `TestMain(m *testing.M)`

**After migration**: Use `BeforeSuite` and `AfterSuite` callbacks

**Benefits**:
- More explicit and declarative setup/teardown
- Better integration with Ginkgo's lifecycle management
- Improved error handling and reporting
- Consistent with Ginkgo best practices

**Example migration pattern**:
```go
// Before (in TestMain)
func TestMain(m *testing.M) {
    setup()
    code := m.Run()
    teardown()
    os.Exit(code)
}

// After (in suite file)
var _ = BeforeSuite(func() {
    setup()
})

var _ = AfterSuite(func() {
    teardown()
})
```

**Files to modify**: `test/e2e/e2e_suite_test.go`, any files with `TestMain` functions

### 6.2 References

- [Ginkgo Documentation](https://onsi.github.io/ginkgo/)
- [Gomega Matchers](https://onsi.github.io/gomega/)
- [Ginkgo CI Configuration](https://onsi.github.io/ginkgo/#recommended-continuous-integration-configuration)
