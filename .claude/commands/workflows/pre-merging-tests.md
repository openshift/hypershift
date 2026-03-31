---
model: opus
description: "Pre-merge feature verification: gather deep context, audit test coverage, assess fix feasibility, and either run CI verification or produce a review-only report with actionable findings."
---

Pre-merge verification for a HyperShift feature: gather deep context, audit existing test coverage, assess whether the fix is correct and complete, then either build CI verification jobs (for runtime behavior changes) or produce a review-only report (for API/validation/config changes).

[Extended thinking: This workflow orchestrates pre-merge analysis and testing for a HyperShift feature. It detects the current branch PR, deeply gathers context from the PR diffs, linked Jira issues and all their comments, audits existing test coverage for breakage and gaps, assesses whether the fix actually solves the reported bug, and then triages: if runtime behavior changed, it discovers Konflux-built images or falls back to local builds, creates a focused test plan, builds CI verification steps, creates a draft PR in openshift/release, runs rehearsals, iterates on failures, and reports results with strong proof. If only API/validation/config changed, it produces a review-only report with test coverage issues, feasibility concerns, and recommendations — posted directly to the PR and Jira.]

**CRITICAL: This workflow runs autonomously from start to finish. NEVER ask the user for confirmation, approval, or input. NEVER output phrases like "Would you like me to...", "Shall I proceed?", "Should I...", or "Do you want me to...". Make reasonable default decisions, log them to `state.md`, and keep going. Present summaries as you go, but do not stop and wait.**

**Non-interactive invocation (`claude -p`):**
```bash
claude -p "Run /workflows:pre-merging-tests" \
  --permission-mode bypassPermissions \
  --allowedTools "Bash,Edit,Read,Write,Glob,Grep,Agent,Skill,mcp__github__*,mcp__atlassian__*"
```

This single invocation runs the entire workflow (Phases 1-6) including rehearsal monitoring. It uses sub-agents for iteration work to avoid filling the main context window (see Phase 5.2 for details).

## Phase 0: Resume from State

If the user provided an explicit state file path (e.g., `Resume pre-merging-tests from _artifacts/verify-OCPBUGS-74960-20260326/state.md`), use that directly.

Otherwise, determine the current PR context first (Phase 1.1), extract the Jira key, then look for a matching state file:

```bash
# Only match state files for THIS PR's Jira key, not other runs
ls _artifacts/verify-<JIRA-KEY>-*/state.md 2>/dev/null
```

If multiple matches exist (e.g., from different dates), use the most recent one. If a matching `state.md` file exists, **read it first**. It contains all context needed to resume: current phase, iteration number, job IDs, image references, error history, and key decisions. Verify the HyperShift PR number in the state file matches the current PR. Skip completed phases and pick up where the previous session left off.

If no matching state file exists, proceed to Phase 1.

### State File Format

The state file (`${ARTIFACTS_DIR}/state.md`) is the single source of truth for resuming after context compaction. **Update it after every significant milestone** (phase completion, iteration result, key decision). It must contain everything needed to continue without re-reading build logs or re-running commands.

```markdown
# Verification State: <Jira-Key>

## Current Status
- **Phase:** <current phase number and name>
- **Verification Path:** <FULL_CI | REVIEW_ONLY>
- **Iteration:** <current iteration number>
- **Last Updated:** <timestamp>
- **Overall Result:** <IN_PROGRESS | PARTIAL_PASS | PASS | BLOCKED | REVIEW_COMPLETE>

## References
- **HyperShift PR:** openshift/hypershift#<number> — <title>
- **Target Branch:** <main or release-X.Y>
- **CI PR:** openshift/release#<number>
- **Jira:** <JIRA-KEY>
- **Artifacts Dir:** <path>

## Images
- **HO Image:** <full image reference or "default">
- **CPO Image:** <full image reference or "default">

## CI Job
- **Job Name:** <full periodic job name>
- **Workflow:** <workflow name>
- **Custom Steps:** <list of custom step names you created>

## Iteration History
### Iteration N — <PASS|FAIL|PARTIAL_PASS> — <timestamp>
- **Job ID:** <prow job ID>
- **Prow URL:** <link>
- **Commit:** <sha> — <message>
- **Result:** <outcome>
- **Root Cause:** <if failed, one-line root cause>
- **Fix Applied:** <if failed, one-line fix description>
- **Key Findings:** <important observations, e.g., "HC private-fs4xj rolled out in 5m, SG verify passed, e2e had transient controlPlaneVersion timeout">

## Decisions Made
- <key decision 1, e.g., "Switched from cli to upi-installer for AWS CLI">
- <key decision 2, e.g., "Using VPC endpoint state check instead of baseline/diff for SG verification">

## Known Issues
- <issue 1, e.g., "controlPlaneVersion has no desired image — transient CI rate limiter issue, not related to fix">

## Next Action
<Exactly what to do next, e.g., "Trigger iteration 5 with /pj-rehearse <full-job-name>, or proceed to Phase 6 reporting if partial pass is acceptable">
```

**When to update the state file:**
- After Phase 1 completes (references, images, feature summary)
- After Phase 1.6 completes (triage decision: FULL_CI or REVIEW_ONLY)
- After Phase 1.7 completes (test coverage audit findings)
- After Phase 1.8 completes (feasibility analysis)
- After Phase 3 completes (test plan, CI job structure) — Full CI path only
- After Phase 4 completes (CI PR created, custom step names) — Full CI path only
- After each iteration completes (result, findings, next action)
- After any key decision or direction change

## Phase 1: Gather Context

### 1.1 Detect Current PR and Target Branch

Find the PR associated with the current branch:

```bash
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
gh pr view "$CURRENT_BRANCH" --repo openshift/hypershift --json number,title,body,commits,files,labels,headRefName,baseRefName
```

If no PR exists for the current branch, inform the user and stop.

**Determine the target branch** from the PR's `baseRefName`:
- `main` → CI config goes in `openshift-hypershift-main.yaml`
- `release-X.Y` (e.g., `release-4.21`) → CI config goes in `openshift-hypershift-release-X.Y.yaml`

Record the target branch in the state file. This affects:
- Which CI config file to modify in Phase 4.4
- The generated job names (e.g., `pull-ci-openshift-hypershift-release-4.21-e2e-...` vs `pull-ci-openshift-hypershift-main-e2e-...`)
- Which branch's code the CI job tests against

**Backport PRs** (base branch is `release-X.Y`): The feature may already be merged to `main` via parent PRs. If so, the release branch images from the nightly/release payload already contain the code — image overrides may not be needed. However, the e2e tests and CI config must target the release branch, not `main`.

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
- **QE-side tests in `openshift-tests-private`**: Search the `openshift/release` repo for existing QE jobs that may already test this feature:
  ```bash
  # Search QE config files for feature-related job names or env vars
  grep -r "<feature-keywords>" ci-operator/config/openshift/openshift-tests-private/ --include="*.yaml" -l
  # List hypershift QE jobs for the target branch
  grep "as:.*hypershift.*<feature-keyword>" ci-operator/config/openshift/openshift-tests-private/openshift-openshift-tests-private-<branch>__*.yaml
  ```
  QE tests may already provide sufficient coverage — if so, you may not need a custom verification job, or you can design yours to complement rather than duplicate QE coverage.

### 1.4 Output Feature Summary

Present the feature summary using this template, then proceed immediately to Phase 1.5:

```
## Feature Summary: <Feature Name>

### Problem Statement
What gap or limitation exists today? Why is this feature needed?

### Solution Approach
- **Architecture**: How the feature works at a high level (controller flow, data flow, component interactions)
- **Key Mechanism**: The core technical approach (e.g., "controller watches X annotation and reconciles Y resource")
- **Trigger → Action → Result**: What triggers the feature, what the controller does, and what the end state looks like

### API Design Decisions
- New/modified fields and their purpose
- Validation rules (CEL, webhooks)
- Defaulting behavior
- Why these design choices were made (from Jira comments and PR discussion)

### PR Scope
- What this specific PR delivers vs the full feature vision
- Which components are touched and why
- What is NOT included (deferred to follow-up work)

### Components Affected
| Component | Changes | Impact |
|-----------|---------|--------|
| API | ... | ... |
| HyperShift Operator | ... | ... |
| Control Plane Operator | ... | ... |
| CLI | ... | ... |

### Related Tests
- Existing e2e tests covering this feature
- Existing unit tests in changed packages
- Gaps in test coverage
```

### 1.5 Save Context to Artifacts

Save the gathered context to the artifacts directory so it persists across iterations and sessions:

```bash
ARTIFACTS_DIR="_artifacts/verify-<jira-key>-$(date +%Y%m%d%H%M%S)"
mkdir -p "${ARTIFACTS_DIR}"
```

Write the following files:
- `${ARTIFACTS_DIR}/feature-summary.md` — The full feature summary from step 1.4
- `${ARTIFACTS_DIR}/pr-context.md` — PR number, title, branch, base branch, changed files list
- `${ARTIFACTS_DIR}/jira-context.md` — Jira issue keys, summaries, status, and key comments/decisions extracted from the issues
- `${ARTIFACTS_DIR}/test-plan.md` — The test plan from Phase 3 (written after Phase 3 completes)
- `${ARTIFACTS_DIR}/state.md` — **Initialize the state file** (see Phase 0 for format). Set phase to "Phase 1 complete", populate references and images sections.

This artifacts directory is reused in Phase 5 for iteration tracking. If the directory was already created (e.g., resuming work), reuse it rather than creating a new one. **Always update `state.md` when completing a phase or iteration.**

### 1.6 Triage Gate: Determine Verification Path

After gathering context and saving artifacts, classify the PR to determine which verification path to follow. This is a critical decision point that avoids wasting CI resources on changes that don't need a custom job.

**Classification criteria:**

| Changed Paths | Change Type | Verification Path |
|---|---|---|
| Only `api/` (types, CEL rules, CRDs) | API/validation change | **Review-Only** → Phase 1.7-1.9 → Phase 6A |
| Only `test/` | Test-only change | **Review-Only** → Phase 1.7 → Phase 6A |
| Only docs, comments, `.work/` | Non-functional change | **Review-Only** → Phase 6A |
| `hypershift-operator/`, `control-plane-operator/`, `support/` | Runtime behavior change | **Full CI** → Phase 2-6 |
| `cmd/` (CLI logic, not just generated CRDs) | CLI behavior change | **Full CI** → Phase 2-6 |
| Mix of API + operator/CPO code | Runtime + API change | **Full CI** → Phase 2-6 |

**Decision rules:**
1. If only generated files changed (CRD manifests, `zz_generated.*`), trace back to the source change. Generated-only changes follow the source type.
2. If unsure, default to **Full CI** — it's better to run a CI job than to miss a runtime issue.
3. The `--dry-run` flag (if passed by user) forces Review-Only regardless of classification.

Record the decision in `state.md` under "Decisions Made."

**If Review-Only path:** Continue to Phase 1.7 (Test Coverage Audit), then 1.8 (Fix Feasibility), then 1.9 (Solve Spec Audit if applicable), then Phase 6A (Review-Only Report).

**If Full CI path:** Continue to Phase 2 (Discover or Build Operator Images).

### 1.7 Test Coverage Audit

Audit existing tests to determine if the PR will break them and identify gaps. This phase runs for **both** verification paths.

#### 1.7.1 Will Existing Tests Break?

Search for tests that reference behavior the PR changes:

1. **Find tests referencing changed constants, error messages, or validation rules:**
   ```bash
   # Extract key strings from the diff (error messages, constant values, validation rules)
   # Search test files for those strings
   grep -r "<old-error-message>" test/ --include="*.go" -l
   grep -r "<old-constant-value>" test/ --include="*.go" -l
   ```

2. **Check if the PR updated those test files:**
   Compare the list of files referencing old behavior against the PR's changed files list. Any test file that references old behavior but is NOT in the changed files list is a **potential breakage**.

3. **For each potentially broken test, verify:**
   - Read the test to confirm it will actually fail (not just reference the string in a comment)
   - Determine the exact fix needed (new expected value, new test input, etc.)
   - Classify severity: **Blocker** (presubmit will fail) vs **Warning** (periodic test, less urgent)

#### 1.7.2 Are Positive Tests Missing?

For the new behavior the PR introduces, check if there's a test that exercises it:

1. **Identify the core new behavior** (e.g., "3 services should now be accepted")
2. **Search for an existing test** that covers it
3. **If missing, describe what the test should do** — include:
   - Test name (following project conventions: "When... it should...")
   - Input setup
   - Expected outcome
   - Which test file it belongs in

#### 1.7.3 Are Existing Test Fixtures Stale?

Check if test fixtures (YAML files, sample objects) reference deprecated or removed values that the PR addresses:

```bash
grep -r "<deprecated-value>" test/ --include="*.yaml" -l
grep -r "<deprecated-value>" test/ --include="*.go" -l | head -20
```

Stale fixtures are low-priority but worth noting.

#### 1.7.4 Output Test Coverage Report

Present findings as a table:

```
## Test Coverage Audit

### Tests That Will Break (Blockers)
| Test File | Line | Test Name | Issue | Required Fix |
|-----------|------|-----------|-------|--------------|
| ... | ... | ... | ... | ... |

### Missing Positive Tests
| New Behavior | Expected Test | Priority |
|-------------|---------------|----------|
| ... | ... | High/Medium/Low |

### Stale Fixtures
| File | Line | Issue | Priority |
|------|------|-------|----------|
| ... | ... | ... | Low |
```

Save to `${ARTIFACTS_DIR}/test-coverage-audit.md`.

### 1.8 Fix Feasibility Analysis

Assess whether the PR's fix actually solves the reported bug and whether it introduces new risks. This phase is especially important for API/validation changes where the fix may be too broad or too narrow.

#### 1.8.1 Does the Fix Match the Bug Scenario?

1. **Reproduce the bug path mentally:**
   - What exact user action triggers the bug? (CLI command, manual YAML, API call)
   - Trace the code path from that action to the failure
   - Does the PR's change actually intercept that code path?

2. **Check if the reported trigger actually hits the changed code:**
   - If the bug says "using the CLI with --flag X", check if the CLI code path actually reaches the validation being changed
   - If the bug says "manually creating YAML", verify the validation is CEL-based (applied at API server) vs webhook-based vs controller-based
   - **Watch for dead code:** Parameters accepted but never used, functions called but results ignored

3. **Can the bug be reproduced with the current codebase?**
   - Check if the CLI/webhook/controller has been updated since the bug was filed
   - The bug may already be partially fixed or the code path may have changed

#### 1.8.2 Is the Fix Scoped Correctly?

1. **Too broad:** Does the fix relax validation for cases beyond the bug scenario?
   - Example: Lowering service count from 4→3 for ALL platforms when only network type `Other` needs it
   - For each case the fix now allows, ask: "Will this succeed at runtime, or just pass validation?"

2. **Too narrow:** Does the fix only address one symptom while the root cause affects other cases?
   - Example: Fixing one platform's validation but the same issue exists for others

3. **Check for safety nets being removed:**
   - If validation is being relaxed, what catches invalid configs that used to be blocked?
   - Are there platform-specific guards that compensate? (e.g., Azure has Ignition CEL rule, but AWS doesn't)
   - List platforms/scenarios where the removed safety net had value

#### 1.8.3 Suggest Alternatives (if applicable)

If the fix is too broad or has gaps, propose alternatives:
- **More targeted fix:** Scope the change to the specific scenario (e.g., network-type-aware CEL rule)
- **Fix + guards:** Apply the broad fix but add compensating guards (e.g., platform-specific CEL rules)
- **Different approach:** If the root cause is elsewhere (e.g., CLI should generate different services for `Other` network type)

#### 1.8.4 Output Feasibility Report

```
## Fix Feasibility Analysis

### Bug Scenario Match
- **Reported trigger:** <how the user hits the bug>
- **Code path:** <trigger → validation → failure>
- **Fix intercepts at:** <where the PR changes the code path>
- **Match assessment:** <EXACT_MATCH | PARTIAL_MATCH | MISMATCH>

### Scope Assessment
- **Fix scope:** <what the fix changes>
- **Bug scope:** <what the bug requires>
- **Gap:** <what the fix allows beyond the bug, or what it misses>

### Risk Matrix
| Scenario | Before Fix | After Fix | Runtime Outcome |
|----------|-----------|-----------|-----------------|
| ... | Rejected/Accepted | Rejected/Accepted | Success/Failure |

### Recommendations
1. ...
2. ...
```

Save to `${ARTIFACTS_DIR}/feasibility-analysis.md`.

### 1.9 Solve Spec Audit (if applicable)

If the PR includes a `.work/jira/solve/spec-*.md` file (generated by `/jira:solve`), audit the spec against the actual implementation. Skip this phase if no spec file exists.

#### 1.9.1 Compare Spec Claims to Reality

For each claim in the spec:
1. **Root cause analysis:** Does the spec correctly identify the root cause? Cross-reference with the actual code.
2. **Solution plan:** Were all planned changes implemented? Were any skipped?
3. **Files to modify:** Were all listed files actually modified? Were unexpected files changed?
4. **Testing phase:** Were the spec's testing recommendations followed?

#### 1.9.2 Check Spec Accuracy

- **Code references:** Are the line numbers and file paths in the spec still accurate? (Code may have shifted since the spec was generated)
- **Behavioral claims:** Does the spec correctly describe what the code does? (e.g., "the CLI generates OVNSbDb" — does it really?)
- **Acceptance criteria:** Are all criteria met by the PR?

#### 1.9.3 Output Spec Audit Report

```
## Solve Spec Audit

### Spec vs Implementation
| Spec Claim | Reality | Status |
|------------|---------|--------|
| ... | ... | CORRECT / INCORRECT / NOT_IMPLEMENTED |

### Spec Accuracy Issues
| Issue | Impact |
|-------|--------|
| ... | ... |

### Unmet Acceptance Criteria
| Criterion | Status | Gap |
|-----------|--------|-----|
| ... | ... | ... |
```

Save to `${ARTIFACTS_DIR}/spec-audit.md`.

## Phase 6A: Review-Only Report

This phase is used when the Triage Gate (Phase 1.6) determined that a custom CI job is not needed. Instead of building CI infrastructure, produce a comprehensive review comment on the PR.

### 6A.1 Compile Report

Combine findings from all completed phases into a single report:

1. **Feature Summary** (from Phase 1.4)
2. **Test Coverage Audit** (from Phase 1.7) — broken tests, missing tests, stale fixtures
3. **Fix Feasibility Analysis** (from Phase 1.8) — scope assessment, risk matrix, recommendations
4. **Solve Spec Audit** (from Phase 1.9, if applicable) — spec vs implementation gaps

### 6A.2 Post Comment on HyperShift PR

Post a structured comment on the PR using `gh pr comment`. Format:

```markdown
## Pre-Merge Analysis: <Jira-Key> — <Title>

### Feature Summary
<1-2 paragraph summary of what the PR does>

### Test Coverage Issues
<Table of broken tests (blockers) and missing tests>

### Fix Feasibility
<Scope assessment, risk matrix, recommendations>

### Solve Spec Gaps (if applicable)
<Spec vs implementation discrepancies>

### Recommendations
<Numbered list of actionable items, ordered by priority>

---
_Analysis generated via `/workflows:pre-merging-tests` review-only path_
```

### 6A.3 Update Jira Issue

Add a comment to the linked Jira issue with a summary of findings using `mcp__atlassian__jira_add_comment`.

### 6A.4 Final Summary

Present the user with:
- Link to the PR comment
- Summary of findings by severity (blockers, warnings, suggestions)
- Whether the PR is ready for review or needs fixes first
- Path to local artifacts directory

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

**Backport PRs:** If the PR targets a release branch (e.g., `release-4.21`) and the feature is already merged to `main`, the release branch nightly images may or may not contain the code yet (depends on whether `main` changes have been cherry-picked). Check whether the PR is still open — if so, the release branch images do NOT contain the code and you need Konflux images or local builds from the release branch. If the parent PRs are merged to `main` but not yet to the release branch, image overrides are needed.

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

### 3.3 Account for Shared CI Environment

The HyperShift CI runs in shared AWS accounts (e.g., `hypershift-aws` cluster profile) where dozens of jobs run in parallel. This has critical implications for verification design:

- **Never use simple baseline/diff comparisons** for cloud resources (security groups, VPCs, endpoints, etc.). Other parallel jobs will create and delete the same types of resources during your test window, causing false positives.
- **Verify resource ownership or orphan state** instead. For example, to check if a security group was leaked:
  - DON'T: count SGs before and after, flag any new ones
  - DO: for each new SG, check if its parent resource (e.g., VPC endpoint) still exists. A truly orphaned SG has no active parent resources.
- **Use `SHARED_DIR` for inter-step data passing.** This is the only reliable way to pass data between CI steps (e.g., baseline recordings, cluster names, resource IDs). Files written to `${SHARED_DIR}/` in one step are available in subsequent steps.
- **Use `best_effort: true` on post/verify steps.** If your verification step is in the `post` phase and you need it to run even when the test step fails (e.g., verifying cleanup after cluster deletion), you MUST set `best_effort: true` in the ref YAML. Without it, post steps are skipped when the test phase fails.

### 3.4 Identify and Test Corner Cases

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

### 3.5 Risk Analysis: Compatibility and Performance

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

Output the full test plan (happy path + corner cases + risk items), save it to `${ARTIFACTS_DIR}/test-plan.md`, and proceed to Phase 3.6.

### 3.6 Determine CI Job Structure

Decide the job structure:
- **Install method**: Self-managed (`hypershift-install` + `hypershift-aws-*`) or MCE (`hypershift-mce-install` + `hypershift-mce-*`). Choose based on what the feature requires and what existing tests use.
- **Workflow name**: `hypershift-<platform>-<feature>` (self-managed) or `hypershift-mce-<platform>-<feature>` (MCE)
- **Steps needed**: setup (with image overrides if needed), verify, teardown
- **Cluster requirements**: platform, NodePool config, etc.
- **Config file variant**: Determined by target branch + install method (see Phase 4.4)

## Phase 4: Build CI Verification

### 4.0 Leverage Existing CI Steps and Workflows

**CRITICAL: Always reuse existing CI chains and workflows rather than reimplementing from scratch.**

Before writing any new CI step:

1. **Search for existing steps** that already do what you need:
   ```bash
   grep -r "<keyword>" ci-operator/step-registry/hypershift/ --include="*.yaml" -l
   ```

2. **Study sibling steps** in the same workflow family. The hypershift CI has distinct step families with different conventions:
   - **`hypershift-aws-*`** chains: Use `from: hypershift-operator`, inherit KUBECONFIG from nested management cluster
   - **`hypershift-hostedcluster-*`** refs: Use `from_image: ci/hypershift-cli:latest`, hardcode KUBECONFIG to shared management cluster
   - **Do not mix patterns** — use the same image and KUBECONFIG approach as sibling steps in the workflow

3. **Check what tools are available in the image.** Different CI images have different tools:
   - **`hypershift-operator`** (`ubi9:latest`): Has `oc` but does NOT have `jq`, `python`, `aws`, `unzip`, etc.
   - **`upi-installer`**: Has AWS CLI pre-installed. **Use this for any step that needs AWS CLI operations** (e.g., describing security groups, VPC endpoints, EC2 instances). Do NOT try to install AWS CLI v2 in other images — `cli` and `hypershift-operator` lack `unzip`.
   - **`cli`**: Has `oc` and basic tools but does NOT have AWS CLI or `unzip`.
   - **Prefer standard shell tools** (`grep`, `sed`, `awk`, `cut`, `sort`) and `oc -o go-template` or `oc -o jsonpath` over `jq`
   - Never assume a tool exists — if a command fails silently behind `|| true`, you get false negatives/positives

4. **Triggering rehearsals:**
   - Pushing code alone may not trigger rehearsals — use `/pj-rehearse <full-job-name>` as a PR comment
   - `/retest` retriggers validation checks but may not create new rehearsal jobs
   - Allow up to 10 minutes for `/pj-rehearse` to create the job

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

Add the job configuration to the **correct config file** in `ci-operator/config/openshift/hypershift/`. The config file depends on **two factors**: the target branch and the install method (self-managed vs MCE).

**Determine the install method:**
- **Self-managed** (default): HyperShift installed via `hypershift install` command. Workflows use `hypershift-aws-*`, `hypershift-kubevirt-*`, etc.
- **MCE** (MultiCluster Engine): HyperShift installed as an MCE addon. Workflows use `hypershift-mce-*` steps. Use MCE when the feature being tested involves MCE-specific code paths, agent-based provisioning, or when the test requires an MCE environment.

**Config file selection:**

| Target Branch | Install Method | Config File |
|---|---|---|
| `main` | Self-managed | `openshift-hypershift-main.yaml` |
| `main` | MCE | `openshift-hypershift-main__mce-multi-version.yaml` |
| `release-X.Y` | Self-managed | `openshift-hypershift-release-X.Y.yaml` |
| `release-X.Y` | MCE (presubmit) | `openshift-hypershift-release-X.Y__mce.yaml` (if exists) |
| `release-X.Y` | MCE (periodic) | `openshift-hypershift-release-X.Y__periodics-mce.yaml` |
| `release-X.Y` | Periodic (non-MCE) | `openshift-hypershift-release-X.Y__periodics.yaml` |

**How to detect MCE vs self-managed:** Check the workflow being used. If it starts with `hypershift-mce-*`, use an MCE config file. If it uses `hypershift-aws-*`, `hypershift-kubevirt-*`, or other non-MCE workflows, use the base config file. Also check what existing CI jobs the feature's e2e tests run in — if they're in MCE jobs, your verification should also use MCE.

**Note:** Not all branch/variant combinations exist. Check `ls ci-operator/config/openshift/hypershift/` to see which config files are available for the target branch before choosing. For example, `__mce` variants exist for releases 4.12-4.19 but not 4.20+, while `__periodics-mce` exists for 4.16+.

The target branch was determined in Phase 1.1 from the PR's `baseRefName`. Using the wrong config file means the job won't run against the correct branch code or install method.

**Important:** The `ci-operator` config's `zz_generated_metadata.branch` field must match the target branch, and the `variant` field must match the config suffix (e.g., `mce`, `periodics-mce`). Verify the `org`, `repo`, `variant`, and `branch` fields match the existing entries in that config file.

Also add `OWNERS` files in new step registry directories.

### 4.5 Regenerate Job Configs

**CRITICAL: You must run `make update` in the openshift/release repo after modifying any ci-operator configs or step registry files.** This regenerates the Prow job YAML files in `ci-operator/jobs/` from the configs in `ci-operator/config/`. Without this step, the rehearsal will not pick up your new job.

```bash
cd <openshift-release-repo>
make update
```

This generates/updates files in `ci-operator/jobs/openshift/hypershift/`. Make sure to `git add` these generated files along with your config and step registry changes.

## Phase 5: Create Draft PR, Run Rehearsal, and Track Iterations

### 5.0 Initialize Iteration Tracking

Reuse the artifacts directory created in Phase 1.5 (or create it now if not already done):

```bash
# Reuse existing ARTIFACTS_DIR from Phase 1.5, or create if needed
ARTIFACTS_DIR="${ARTIFACTS_DIR:-_artifacts/verify-<jira-key>-$(date +%Y%m%d%H%M%S)}"
mkdir -p "${ARTIFACTS_DIR}"
```

Create an `iterations.md` file to serve as a running log:

```markdown
# Verification Iterations: <Jira-Key> - <Feature Name>

## Summary
- **PR:** openshift/release#<number>
- **Job:** periodic-ci-openshift-hypershift-<version>-periodics-<job-name>
- **Started:** <timestamp>
```

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

Use a **sub-agent polling loop** to monitor and iterate. Each iteration runs in a separate sub-agent with its own context window, keeping the main context lean. **Do not exit or ask questions — run the loop until the job completes or fails.**

Here is the concrete implementation — follow this exactly:

```
# Step A: Initial wait (rehearsals take time to start)
sleep 300  (5 minutes for job to be picked up)

# Step B: Polling loop
MAX_POLLS=30  (5 hours max at 10-minute intervals)
for poll in 1..MAX_POLLS:

  # Spawn a sub-agent to check status
  Agent(prompt="Check rehearsal job status for openshift/release PR #<number>.
    Run: gh pr checks <number> --repo openshift/release | grep -i rehearse

    If the job is still pending or running, respond with exactly: STATUS: PENDING
    If the job passed, respond with exactly: STATUS: PASS
    If the job failed, respond with exactly: STATUS: FAIL

    Also read and update <artifacts-dir>/state.md with current status.")

  # Read the sub-agent result
  If result contains "STATUS: PENDING":
    sleep 600  (10 minutes)
    continue

  If result contains "STATUS: PASS":
    # Spawn sub-agent to analyze results
    Agent(prompt="The rehearsal job for openshift/release PR #<number> PASSED.
      1. Download the build log for the custom verify step from GCS
         (decompress with gunzip if needed)
      2. Extract [PASS]/[FAIL]/[SKIP] lines
      3. Check finished.json for each step
      4. Save results to <artifacts-dir>/iterations.md
      5. Update <artifacts-dir>/state.md with Overall Result: PASS
      6. Return the full test output and step results.")
    break → proceed to Phase 6

  If result contains "STATUS: FAIL":
    # Spawn sub-agent to analyze failure and fix
    Agent(prompt="The rehearsal job for openshift/release PR #<number> FAILED.
      1. Download the build log from GCS (decompress with gunzip if needed)
      2. Identify which step failed and the root cause
      3. Classify: real failure, transient flake, or partial pass (see Phase 5.4)
      4. If partial pass (custom verify passed, e2e flaked):
         - Save results to <artifacts-dir>/iterations.md
         - Update <artifacts-dir>/state.md with Overall Result: PARTIAL_PASS
         - Return 'STATUS: PARTIAL_PASS' with evidence
      5. If real failure:
         - Fix the CI step in openshift/release repo
         - Commit, push, trigger new rehearsal by commenting `/pj-rehearse <full-job-name>` on the CI PR
         - Update <artifacts-dir>/state.md and iterations.md
         - Return 'STATUS: FIXED — retriggered iteration N'
      6. If transient flake with no custom verify evidence:
         - Retrigger by commenting `/pj-rehearse <full-job-name>` on the CI PR
         - Return 'STATUS: RETRIGGER'")

    If sub-agent result contains "PARTIAL_PASS":
      break → proceed to Phase 6
    If sub-agent result contains "FIXED" or "RETRIGGER":
      continue loop (will check new job on next poll)
```

**Why sub-agents?** Each sub-agent gets a **fresh context window**. Build log analysis, error diagnosis, and fix iterations stay in the sub-agent's context — the main agent only sees a short result summary. This prevents context exhaustion during long monitoring periods with multiple iterations.

**Queue time expectations:** Jobs requiring cluster profiles (e.g., `hypershift-aws`) can sit in "pending" for 30 minutes to several hours waiting for resources. This is normal — the polling loop handles it automatically.

### 5.3 Save Iteration Results

After each rehearsal completes (pass or fail), save the results to the artifacts directory:

```bash
ITER_NUM=1  # increment for each iteration
ITER_DIR="${ARTIFACTS_DIR}/iteration-${ITER_NUM}"
mkdir -p "${ITER_DIR}"

# Save the build log
gsutil cat "<gcs-path>/build-log.txt" | gunzip > "${ITER_DIR}/build-log.txt" 2>/dev/null || \
  gsutil cat "<gcs-path>/build-log.txt" > "${ITER_DIR}/build-log.txt"

# Save step results for each step that ran
for step_dir in $(gsutil ls "<gcs-path>/artifacts/<job-name>/"); do
  step_name=$(basename "${step_dir}")
  mkdir -p "${ITER_DIR}/steps/${step_name}"
  gsutil cat "${step_dir}finished.json" > "${ITER_DIR}/steps/${step_name}/finished.json" 2>/dev/null || true
done

# Extract test output (pass/fail/skip lines)
grep -E "^\[PASS\]|\[FAIL\]|\[SKIP\]|RESULT:|=== Step" "${ITER_DIR}/build-log.txt" > "${ITER_DIR}/test-summary.txt" 2>/dev/null || true
```

Append the iteration record to `iterations.md`:

```markdown
## Iteration <N> — <PASS/FAIL/PARTIAL_PASS> — <timestamp>
- **Job ID:** <prow-job-id>
- **Prow URL:** <link>
- **Commit:** <sha> — <commit message>
- **Result:** <PASS/FAIL/PARTIAL_PASS>
- **Failure Reason:** <root cause if failed, e.g., "jq not found in hypershift-operator image">
- **Fix Applied:** <what was changed, e.g., "switched to oc go-template">

### Test Output
\```
<paste [PASS]/[FAIL]/[SKIP] lines and summary>
\```

### Step Results
| Step | Result |
|------|--------|
| ipi-install-rbac | SUCCESS |
| create-management-cluster | SUCCESS |
| ... | ... |
```

**After saving iteration results, always update `${ARTIFACTS_DIR}/state.md`** with the iteration outcome, key findings, decisions made, known issues, and the next action. This ensures a new session can resume without re-reading build logs.

### 5.4 Iterate on Failures

**If PASSED:**
- Extract `[PASS]`, `[FAIL]`, `[SKIP]` lines with evidence
- Save final iteration to artifacts
- Update `state.md` with `Overall Result: PASS`
- Proceed to Phase 6

**If FAILED — classify the failure type before deciding next action:**

1. **Real failure (CI step/script issue):** Missing tools, wrong image, syntax errors, incorrect commands.
   - Save the failed iteration to artifacts (step 5.3)
   - Document the failure reason and fix in `iterations.md`
   - Fix the issue, commit, push
   - Trigger new rehearsal by commenting `/pj-rehearse <full-job-name>` on the CI PR (e.g., `gh pr comment <number> --repo openshift/release --body "/pj-rehearse <full-job-name>"`)
   - Update `state.md` with `Overall Result: IN_PROGRESS` and the fix details
   - Increment iteration counter and return to step 5.2

2. **Transient CI flake:** Rate limiter exhaustion, condition validation timeouts, infrastructure issues. Common examples:
   - `client rate limiter Wait returned an error: context deadline exceeded`
   - `controlPlaneVersion has no desired image` (condition check times out despite successful rollout)
   - Network timeouts to cloud APIs

   Signs of a transient flake: the cluster actually deployed/rolled out successfully (check the logs for "Successfully waited for..." messages), but a later validation step timed out. **Do not iterate on transient flakes** — document them and check if the custom verification steps (the ones you wrote) passed independently.

3. **Partial pass:** The overall job failed but your custom verification steps passed. This is common when:
   - The e2e test has a transient failure but your post/verify step (with `best_effort: true`) ran and passed
   - The cluster lifecycle completed successfully but condition validation timed out

   A partial pass can be sufficient evidence if: (a) the cluster lifecycle completed (create, rollout, destroy), (b) your custom verification steps passed, and (c) the failure is in a step you didn't write and is a known flaky test. Update `state.md` with `Overall Result: PARTIAL_PASS` and proceed to Phase 6 with appropriate caveats in the report.

## Phase 6: Report Results

### 6.1 Extract Structured Results

From the build log, extract results and format into a markdown table with step numbers, checks, results, and evidence.

**For partial-pass results:** Clearly separate the results into:
- **Custom verification steps** (the ones you wrote): report their individual PASS/FAIL status
- **Standard e2e test steps** (existing tests): note if they failed and whether the failure was transient
- **Overall assessment:** state whether the verification evidence is sufficient despite the partial failure, and explain why

### 6.2 Update openshift/release PR

Update the PR description with the full results table using `gh pr edit`. Include:
- Summary of what the job verifies
- Results table with evidence for each check
- Link to the successful rehearsal job
- Image overrides used (if any)

### 6.3 Comment on HyperShift PR

Post a comment on the HyperShift PR with the verification results using `gh pr comment`. The comment should include:
- Results table with evidence
- Link to the rehearsal job and CI PR
- **End the comment with `/verified by <USER>`** where `<USER>` is the GitHub username of the person who requested the verification (get it via `gh api user --jq .login` or from the PR author). This marks the PR as verified.

### 6.4 Update Jira Issue

Add a comment to the linked Jira issue with verification results using `mcp__atlassian__jira_add_comment`. Include:
- Results table
- Links to the rehearsal job and both PRs
- Which operator images were tested

### 6.5 Final Summary

Present the user with:
- Links to both PRs
- Full results summary
- Suggested next steps (request reviews, squash commits, mark PR ready)
- Path to local artifacts directory with all iteration history: `${ARTIFACTS_DIR}/`

The artifacts directory provides a complete audit trail:
```
_artifacts/verify-OCPBUGS-12345-20260326120000/
├── iterations.md              # Running log of all iterations with root causes and fixes
├── iteration-1/
│   ├── build-log.txt          # Full build log
│   ├── test-summary.txt       # Extracted [PASS]/[FAIL]/[SKIP] lines
│   └── steps/                 # Per-step finished.json results
├── iteration-2/
│   └── ...
└── iteration-N/               # Final successful run
    └── ...
```
