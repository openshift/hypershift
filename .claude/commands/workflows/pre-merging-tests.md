---
model: opus
description: "Pre-merge feature verification through CI: gather deep context from the current branch PR and Jira, discover or build operator images, create a focused test plan targeting core feature logic, build CI jobs, run rehearsals, iterate on failures, and report results with strong evidence."
---

Pre-merge verification for a HyperShift feature: discover or build operator images, create focused CI verification targeting the core feature logic, run rehearsals, and report results with strong evidence.

[Extended thinking: This workflow orchestrates pre-merge testing for a HyperShift feature. It detects the current branch PR, deeply gathers context from the PR diffs, linked Jira issues and all their comments, discovers Konflux-built images or falls back to local builds, creates a focused test plan targeting the core logic of the feature, builds CI verification steps leveraging existing install/create steps with proper image overrides, creates a draft PR in openshift/release, runs rehearsals, iterates on failures, and reports results with strong proof back to the PR and Jira.]

## Phase 1: Gather Context

### 1.1 Detect Current PR

Find the PR associated with the current branch:

```bash
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
gh pr view "$CURRENT_BRANCH" --repo openshift/hypershift --json number,title,body,commits,files,labels,headRefName,baseRefName
```

If no PR exists for the current branch, inform the user and stop.

### 1.2 Extract and Read Jira Issues

Extract Jira issue keys from the PR title, body, and branch name (patterns: `OCPSTRAT-\d+`, `CNTRLPLANE-\d+`, `OCPBUGS-\d+`).

For each Jira key found:
- Fetch the issue details using `mcp__atlassian__jira_get_issue`
- **Read all comments** on the issue — comments often contain design decisions, acceptance criteria, review feedback, and context that isn't in the description
- If the issue has parent/child links (epic, feature, story), fetch those too to understand the broader context
- Collect any linked PRs mentioned in comments or description

### 1.3 Deep Feature Analysis

Use Agent tool with subagent_type="Explore" to read and understand **all changed files** in the PR. For each changed file, understand:
- What was the behavior before?
- What is the new behavior?
- What is the core logic that makes this feature work?

Identify which components are affected:
- **API changes** (`api/`): New fields, types, CEL validation rules
- **Control plane** (`control-plane-operator/`): New controllers, reconcilers, hosted cluster config
- **Data plane** (`hypershift-operator/controllers/nodepool/`): NodePool, Machine changes
- **Cloud provider** (platform-specific dirs): AWS, Azure, GCP integration
- **CLI** (`cmd/`): New commands or flags

Also collect:
- Related e2e tests: `grep -r "<feature-keywords>" test/e2e/ --include="*.go" -l`
- Related unit tests in the changed packages
- Any existing CI jobs that exercise this code path
- Design docs or enhancement proposals referenced in the Jira issue

Present a comprehensive feature summary to the user before proceeding.

## Phase 2: Discover or Build Operator Images

### 2.1 Try Konflux Images First

Use the `/find-konflux-images` skill to discover Konflux-built container images from the PR. Konflux automatically builds images for PRs and these can be used directly in CI.

Look for images for:
- `hypershift-operator`
- `control-plane-operator`

### 2.2 Fallback: Build and Push Locally

If Konflux images are not available (e.g., Konflux not configured, build failed, or images expired):

Ask the user for their quay.io repository (e.g., `quay.io/<username>/hypershift`), then build and push:

```bash
# Build hypershift-operator image
make build-image RUNTIME=podman IMAGE_TAG=<quay-repo>:ho-<branch>
podman push <quay-repo>:ho-<branch>

# Build control-plane-operator image
podman build -f Dockerfile.control-plane --platform linux/amd64 -t <quay-repo>:cpo-<branch> .
podman push <quay-repo>:cpo-<branch>
```

Record the image references for use in Phase 4.

### 2.3 Determine Which Images Need Override

Based on the changed files in the PR, determine which operator images need to be overridden in the CI job:

| Changed Path | Image Override Needed |
|---|---|
| `hypershift-operator/` | hypershift-operator |
| `control-plane-operator/` | control-plane-operator |
| `control-plane-pki-operator/` | control-plane-operator (same image) |
| `support/` | Both (shared utilities) |
| `api/` | Both (API types used by both) |
| `cmd/` | Depends on which cmd |
| `test/e2e/` only | No override needed (test-only changes) |

If no operator code changed (e.g., only test or CI changes), no image overrides are needed.

## Phase 3: Create Test Plan

### 3.1 Focus on Core Feature Logic

Based on the deep context gathered in Phase 1, identify the **core logic** of the feature — the essential behavior that must work for the feature to be considered functional. Skip superficial checks; focus on high-value verification that proves the feature works.

Ask: "If I could only run 5 checks, which ones would give the strongest proof that this feature works?"

Categories to consider (prioritized):
1. **End-to-end functional proof**: Does the feature actually do what it claims? (highest value)
2. **Controller behavior**: Does the controller react correctly to the trigger condition?
3. **Resource state transitions**: Do resources move through expected states?
4. **Data flow**: Does data flow correctly between components (e.g., annotations, labels, env vars)?
5. **Validation rules**: Do API guards prevent invalid configurations?

### 3.2 Design Verification Steps with Strong Evidence

For each check, define:
- **Step name**: Short descriptive name
- **Core logic tested**: Which part of the feature's core logic does this prove?
- **How to verify**: The `oc` commands that produce concrete evidence
- **Pass criteria**: Unambiguous condition
- **Evidence captured**: Specific output that serves as proof (not just "resource exists" but "resource has field X with value Y because controller Z set it")

Strong evidence means:
- Show the actual field values, not just that a resource exists
- Show cause-and-effect (trigger happened -> controller acted -> state changed)
- Show the specific annotation/label/status that proves the controller logic ran
- For replacement/new resources, verify by **name** not just count

### 3.3 Identify and Test Corner Cases

Beyond the happy path, identify corner cases that could break the feature or cause regressions:

- **Invalid input**: What happens with malformed or missing fields? Do CEL rules or webhooks reject them with clear errors?
- **Race conditions**: What if the trigger fires before the controller is ready, or multiple triggers fire simultaneously?
- **Idempotency**: Does the controller handle being called multiple times on the same resource without side effects?
- **Rollback / recovery**: What happens if a step in the feature's flow fails midway? Does the system recover or get stuck?
- **Scale boundaries**: Does the feature work with 0 replicas? With the maximum? With mixed node pools?
- **Interaction with other features**: Does enabling this feature conflict with or break other features (e.g., spot + capacity reservations, upgrades + feature gates)?
- **Deletion / cleanup**: Are resources properly cleaned up when the feature is disabled or the cluster is deleted?
- **Upgrade path**: Does the feature work when upgrading from a version without it to a version with it?

For each corner case, decide whether it should be:
- A verification step in the CI job (if testable with `oc` commands)
- A unit test in the hypershift repo (if it tests internal logic)
- Noted as a known limitation (if not feasible to test in CI)

### 3.4 Risk Analysis: Compatibility and Performance

Before finalizing the test plan, assess the feature's risk profile:

**Compatibility risks:**
- Does this feature change existing API fields or behavior? Could it break existing HostedClusters or NodePools on upgrade?
- Does it introduce new required fields or change defaults? Are existing resources still valid without migration?
- Does it affect shared components (e.g., `support/` utilities, RBAC, webhooks) used by other features?
- Does it change CRD schemas in ways that could break older controllers or clients?
- Is it gated behind a feature gate or annotation, or always-on? Always-on changes carry higher compatibility risk.

**Performance risks:**
- Does it add new controllers or reconcile loops? What is the expected reconciliation frequency?
- Does it add new API calls per reconciliation (e.g., cloud provider calls, kube API calls)? Could this cause throttling at scale?
- Does it increase memory or CPU usage for the operator pods (e.g., new caches, watchers, large data structures)?
- Does it add latency to the critical path (e.g., cluster creation, node provisioning, upgrade flow)?
- Does it create new resources per HostedCluster or per Node? What is the resource count at scale (e.g., 1000 clusters, 100 nodes per cluster)?

For each identified risk:
- **High risk**: Must be verified in the CI job or flagged as a blocker
- **Medium risk**: Should have a verification step or be documented as a known concern
- **Low risk**: Note for awareness, no action needed

Include high and medium risk items as verification steps or acceptance criteria in the test plan.

Output the full test plan (happy path + corner cases + risk items) and ask the user to confirm before proceeding.

### 3.3 Determine CI Job Structure

Decide the job structure:
- **Workflow name**: `hypershift-<platform>-<feature>`
- **Steps needed**: setup (with image overrides if needed), verify, teardown
- **Cluster requirements**: platform, NodePool config, etc.

## Phase 4: Build CI Verification

### 4.1 Leverage Existing Steps for Image Overrides

Do NOT implement image overrides manually. Use the existing CI step registry infrastructure:

**Hypershift-operator image override:**

| Install Path | How to Override |
|---|---|
| `hypershift-install` (self-managed) | Add `OVERRIDE_HO_IMAGE` env var to the step. If not yet supported, add it as a small enhancement in the same PR: in the commands script, check `if [[ -n "${OVERRIDE_HO_IMAGE:-}" ]]; then OPERATOR_IMAGE="${OVERRIDE_HO_IMAGE}"; fi` before the install command. Add the env var to the ref.yaml. |
| `hypershift-mce-install` (MCE) | Already supports `OVERRIDE_HO_IMAGE` env var. Creates ConfigMap `hypershift-override-images` in `local-cluster` namespace; addon controller updates the HO deployment. |

**Control-plane-operator image override:**

| Create HC Path | How to Override |
|---|---|
| `hypershift-mce-agent-create-hostedcluster` (MCE) | Already supports `OVERRIDE_CPO_IMAGE` env var (see [openshift/release#75652](https://github.com/openshift/release/pull/75652)). Passes `--annotations=hypershift.openshift.io/control-plane-operator-image=<image>` to `hcp create cluster`. |
| `hypershift-hostedcluster-create-hostedcluster` (self-managed) | Use `EXTRA_ARGS` env var: `EXTRA_ARGS: "--annotations=hypershift.openshift.io/control-plane-operator-image=<cpo-image>"` |

### 4.2 Create Step Registry Files

Create the step registry files in the openshift/release repo:

```
ci-operator/step-registry/hypershift/<platform>/<feature>/
├── hypershift-<platform>-<feature>-workflow.yaml
├── setup/
│   └── hypershift-<platform>-<feature>-setup-commands.sh
├── verify/
│   └── hypershift-<platform>-<feature>-verify-commands.sh
└── teardown/ (if needed)
    └── hypershift-<platform>-<feature>-teardown-commands.sh
```

The workflow should reuse existing steps where possible (e.g., `hypershift-install`, `hypershift-hostedcluster-create-hostedcluster`) and only add custom setup/verify/teardown steps for the feature-specific logic.

### 4.3 Verify Script Conventions

- Use `set -euo pipefail`
- Define pass/fail/skip helper functions:
  ```bash
  PASS_COUNT=0; FAIL_COUNT=0; SKIP_COUNT=0
  pass() { echo "[PASS] $1"; PASS_COUNT=$((PASS_COUNT + 1)); }
  fail() { echo "[FAIL] $1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }
  skip() { echo "[SKIP] $1"; SKIP_COUNT=$((SKIP_COUNT + 1)); }
  ```
- Group checks under `--- Step N: Description ---` headers
- Capture and echo evidence with each pass/fail
- Print summary at the end with counts and `RESULT: ALL CHECKS PASSED` or exit 1

### 4.4 Create CI Config and OWNERS

- Add the job configuration to the appropriate file in `ci-operator/config/openshift/hypershift/`
- Add `OWNERS` files in new step registry directories

## Phase 5: Create Draft PR and Run Rehearsal

### 5.1 Create Branch, Commit, and Draft PR

In the openshift/release repo:

```bash
git checkout -b verify-<jira-key>-<feature> main
git add ci-operator/
git commit -m "Add <feature> verification job for HyperShift (<jira-key>)"
git push -u origin <branch>
gh pr create --draft --title "..." --body "..."
```

### 5.2 Monitor Rehearsal Job

Poll the rehearsal job status:
```bash
gh pr checks <pr-number> --repo openshift/release
```

Wait for the rehearsal to complete.

### 5.3 Analyze Results

**If PASSED:**
- Fetch the build log from GCS:
  ```bash
  gsutil cat "<gcs-path>/build-log.txt" | gunzip
  ```
- Extract `[PASS]`, `[FAIL]`, `[SKIP]` lines with evidence
- Proceed to Phase 6

**If FAILED:**
- Fetch the build log and identify the failure
- Fix the issue, commit, push
- Return to step 5.2 to monitor the new rehearsal
- Iterate until all checks pass

## Phase 6: Report Results

### 6.1 Extract Structured Results

From the successful build log, extract results and format into a markdown table with step numbers, checks, results, and evidence.

### 6.2 Update openshift/release PR

Update the PR description with the full results table using `gh pr edit`. Include:
- Summary of what the job verifies
- Results table with evidence for each check
- Link to the successful rehearsal job
- Image overrides used (if any)

### 6.3 Update Jira Issue

Add a comment to the linked Jira issue with verification results using `mcp__atlassian__jira_add_comment`. Include:
- Results table
- Links to the rehearsal job and both PRs
- Which operator images were tested

### 6.4 Final Summary

Present the user with:
- Links to both PRs
- Full results summary
- Suggested next steps (request reviews, squash commits, mark PR ready)
