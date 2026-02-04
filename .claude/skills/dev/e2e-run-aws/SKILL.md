---
name: E2E Test Runner
description: "Provides the ability to run and iterate on HyperShift e2e tests. Auto-applies when implementing features that require e2e validation, fixing e2e test failures, or working on tasks that need live cluster testing."
---

# HyperShift E2E Test Runner

This skill enables autonomous iteration on e2e tests - running tests, analyzing failures, making fixes, and re-running until tests pass.

## When to Use This Skill

This skill automatically applies when:
- Implementing a feature that needs e2e test validation
- Fixing a failing e2e test
- Working on a task where the user wants you to iterate until tests pass
- Debugging test failures in the `test/e2e/` directory
- The user mentions running e2e tests or validating changes against a live cluster

## Prerequisites

Source the environment file before using this skill:

```bash
source dev/claude-env.sh
```

## Environment Configuration

Environment variables from `dev/claude-env.sh`:

| Variable | Description |
|----------|-------------|
| `E2E_PLATFORM` | Test platform (AWS, Azure, etc.) |
| `AWS_CREDENTIALS` | Path to AWS credentials file |
| `OIDC_BUCKET` | S3 bucket for OIDC |
| `BASE_DOMAIN` | Base DNS domain |
| `PULL_SECRET` | Path to pull secret file |
| `AWS_REGION` | AWS region |
| `E2E_ARTIFACT_DIR` | Directory for test artifacts |
| `MGMT_KUBECONFIG` | Path to management cluster kubeconfig |
| `CPO_IMAGE_REPO` | Custom CPO image repository |
| `RUNTIME` | Container runtime (podman/docker) |

## Running E2E Tests

### Step 1: Check if Test Binary Needs Rebuilding

**CRITICAL**: Before running any e2e test, you MUST check if the test binary needs rebuilding:

```bash
# Check if binary exists
if [ ! -f ./bin/test-e2e ]; then
  echo "Test binary missing, building..."
  make e2e
fi

# Check if any test files are newer than the binary
NEWEST_TEST=$(find test/e2e -name "*.go" -newer ./bin/test-e2e 2>/dev/null | head -1)
if [ -n "$NEWEST_TEST" ]; then
  echo "Test files changed (e.g., $NEWEST_TEST), rebuilding..."
  make e2e
fi
```

### Step 2: Run the Test

Build and execute the test command:

```bash
KUBECONFIG=$MGMT_KUBECONFIG \
./bin/test-e2e -test.v -test.timeout 2h \
  -test.run "TEST_PATTERN" \
  -test.v \
  --e2e.platform $E2E_PLATFORM \
  --e2e.aws-credentials-file $AWS_CREDENTIALS \
  --e2e.aws-oidc-s3-bucket-name $OIDC_BUCKET \
  --e2e.base-domain $BASE_DOMAIN \
  --e2e.pull-secret-file $PULL_SECRET \
  --e2e.aws-region $AWS_REGION \
  --e2e.artifact-dir $E2E_ARTIFACT_DIR
```

### Step 3: Add Custom CPO Image (When Testing Control Plane Changes)

If you've made changes to control-plane-operator code and built a custom image, add:

```bash
-e2e.control-plane-operator-image $CPO_IMAGE_REPO:TAG
```

## Iteration Loop

When working autonomously on a task that requires e2e validation:

### 1. Initial Test Run
Run the test to establish baseline:
```bash
KUBECONFIG=$MGMT_KUBECONFIG ./bin/test-e2e -test.v -test.run "TestName" [flags...]
```

### 2. On Failure - Analyze
- Read the test output carefully
- Check artifacts in `$E2E_ARTIFACT_DIR/` directory for:
  - Pod logs
  - Events
  - Resource states
- Identify the root cause

### 3. Make Fixes
- Edit the relevant code (test code, operator code, etc.)
- If you modified `test/e2e/*.go` files, the binary will be rebuilt automatically on next run

### 4. Rebuild Images (If Needed)
If you modified control-plane-operator code:
Use the build-cpo-image skill to build and push a new image.

```bash
$RUNTIME build -f Dockerfile.control-plane --platform linux/amd64 -t $CPO_IMAGE_REPO:NEW_TAG .
$RUNTIME push $CPO_IMAGE_REPO:NEW_TAG
```

### 5. Re-run Test
Run the test again with updated code/images. Repeat until passing.

## Common Test Patterns

| Test Pattern | Description |
|--------------|-------------|
| `TestNodePool` | All NodePool tests |
| `TestNodePool/HostedCluster0/Main/TestSpotTerminationHandler` | Specific spot test |
| `TestNodePool.*Karpenter` | All Karpenter-related tests |
| `TestCreateCluster` | Cluster creation tests |
| `TestUpgrade` | Upgrade tests |

## Analyzing Test Failures

### Check Test Output
The test output includes:
- Test name and status
- Assertion failures with expected vs actual
- Timeout information
- Resource creation/deletion logs

### Check Artifact Directory
After a test failure, examine:
```bash
ls -la $E2E_ARTIFACT_DIR/
# Contains: cluster manifests, pod logs, events, resource dumps
```

### Common Failure Patterns

| Pattern | Likely Cause |
|---------|--------------|
| `context deadline exceeded` | Resource didn't reach expected state in time |
| `not found` | Resource wasn't created or was deleted prematurely |
| `connection refused` | Service not ready or network issue |
| `forbidden` | RBAC or permission issue |

## Building Test Binary

When test code changes, rebuild:
```bash
make e2e
```

This compiles `./bin/test-e2e` with all tests from `test/e2e/`.

## Notes

- Tests typically take 10-30+ minutes depending on complexity
- Some tests create real AWS resources (costs money, needs cleanup on failure)
- Use `-test.timeout` to set appropriate timeouts (default: 2h)
- The artifact directory is overwritten on each run
- For long tests, consider running in background and checking periodically
