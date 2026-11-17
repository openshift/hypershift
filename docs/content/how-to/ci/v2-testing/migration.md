# Migrating from V1 to V2

## Procedure for Porting a Test for One Platform

A test's meaning depends on its setup, guards, shared assertions, and execution
history. Programmatic analysis can expose that structure, but deciding whether a
different test preserves its guarantees requires a specification the code itself
does not provide. This procedure makes that judgment explicit and auditable
through small, reviewed transfers of responsibility, with progress measured by
concrete retirement of v1 execution.

Use this procedure after a person or tool identifies a v1 test or code region to
port for a particular platform. The unit of migration is a bounded transfer of
test responsibility: introduce any missing v2 replacement and retire the
corresponding v1 execution for that platform in the same commit. Existing v2
coverage can serve as the replacement when the review establishes that it is
sufficient.

The procedure produces a source change and a review record. It does not require
new test identities or a semantic coverage database. Acceptance records a judgment
that the replacement is sufficient for the stated scope; it does not prove
general equivalence between the suites.

### 1. Define the Source and Platform Scope

Identify the v1 registration or code region and the platform whose execution will
be retired. Describe the behavior it currently exercises using concrete source
references. Identify the relevant platform configurations and execution contexts
in which the replacement must run. If v2 does not yet provide those contexts,
establish them or narrow the proposed retirement.

Keep the transfer small enough that a reviewer can understand everything removed
and where its responsibilities go. One v1 test may require several v2 specs, and
several v1 checks may already be provided by one v2 scenario.

### 2. Trace the Effective V1 Test

Inspect the selected code together with its setup, manifest builders, shared
helpers, before/after hooks, guards, and cleanup. Establish which assertions and
scenario responsibilities would disappear if the selected v1 execution stopped.

Describe what the implementation actually verifies. Distinguish fatal assertions,
nonfatal test errors, and log-only observations. Account for skips and early
returns: checks before a skipped operation may still execute, and a skip inside a
nested subtest may still allow the caller's later checks.

### 3. Choose an Independently Transferable Boundary

State what moves to v2 and what remains in v1. Retain shared code needed by other
callers or platforms. If skipping an enclosing test would also remove unrelated
checks, provide their replacements or narrow the skip boundary. Do not claim the
transfer is complete while responsibilities removed by that boundary remain
unaccounted for.

Physical deletion of a shared helper is not required when other callers still
need it. Retirement concerns the selected platform's execution, rather than
removing every supporting source file.

### 4. Establish the V2 Replacement

Implement missing behavior or identify existing v2 behavior that provides the
responsibility. Preserve the relevant prerequisites, stimulus, assertions,
failure handling, and lifecycle context. For example, readiness after terminating
a worker has a different purpose from initial readiness even if both use the same
predicate.

Place responsibilities in the appropriate v2 test or lifecycle component and
follow the [v2 test conventions](writing-tests.md). Explain meaningful differences
in the replacement. Record suspected v1 defects separately rather than silently
strengthening or weakening the contract during translation. Intentional changes
or omissions require an explicit rationale in the review record.

### 5. Validate the Replacement

Run the relevant v2 behavioral tests in the agreed platform contexts. Inspect
selection, skips, early exits, and results to establish whether the intended
behavior was exercised. Verify that transferred blocking checks still fail the
relevant CI job on assertion failure, considering spec labels, result handling,
and job configuration. Distinguish an assertion failure reported by an informing
spec from a genuine precondition skip or a successful assertion; process exit
status alone is insufficient.

Where practical, introduce a controlled failure to demonstrate that the
replacement detects a relevant defect. Record what each experiment establishes.
A successful run against a healthy system alone does not demonstrate equivalent
defect detection.

Record the commands, results, and any unavailable validation. Disclose uncertainty
so reviewers can assess it rather than treating an unexecuted or skipped path as
validated.

### 6. Retire the Corresponding V1 Execution Atomically

In the same commit as any required replacement, delete the transferred code or
exclude its execution for the selected platform. Inspect the complete change to
confirm that other platforms and unmigrated scenarios retain their required
assertions, setup, and cleanup. Avoid unrelated changes that obscure the transfer.

Preserve the enforcement status of transferred responsibilities. A v2 replacement
for blocking v1 behavior must be blocking when the corresponding v1 execution is
retired. For transfers under this procedure, preserving existing blocking
enforcement is an exception to the default that new v2 tests start informing.

Apply the informing and soaking policy to net-new behavior. Separate additional
assertions from the transferred blocking checks where necessary so they can soak
without weakening existing enforcement. If the replacement has implementation
bugs or instability, fix them or postpone retiring the blocking v1 execution.

Any deliberate reduction in enforcement requires a separate, explicit review
decision with a rationale; it must not be an incidental consequence of porting.

### 7. Review and Accept the Transfer

The reviewer examines both the replacement and everything the retirement removes.
For each responsibility removed from v1, the change must identify its v2 location
or an intentional omission with a rationale. Review the supporting evidence,
remaining uncertainty, and effects on execution and failure reporting.

Use this review record in the PR description:

```markdown
### Source and platform
V1 source references, selected platform, execution contexts, and retirement boundary.

### Replacement and preservation argument
V2 source references and how prerequisites, actions, assertions, and lifecycle
responsibilities are preserved, including existing and replacement enforcement
status.

### Differences and uncertainty
Intentional changes or omissions, effects on failure gating, responsibilities
remaining in v1, and unresolved concerns. State explicitly when there are none.

### Validation
Commands, execution contexts, results, skips, behavioral experiments, and checks
that were unavailable or not run.
```

### Commit Traceability

Use a consistent subject so migration commits are searchable:

```text
test(e2e): port <behavior> from v1 to v2 on <platform>
```

Include the following fields in the commit body, together with the explanation of
why and how the change transfers responsibility:

```text
V1-Source: <repository-relative path> — <function or subcase>
V2-Replacement: <repository-relative path> — <spec or lifecycle responsibility>
Platform: <platform>
Retirement: <what is deleted or excluded, and what remains>
```

Repeat source or replacement fields when the transfer spans multiple locations.
Use paths and symbols or scenario names; line numbers are optional because they
drift. The v1 references identify source in the commit's parent revision, and the
v2 references identify the replacement in the resulting revision.

Keep the detailed preservation argument and validation evidence in the PR
description. Follow repository commit-message conventions, including the
`Signed-off-by` footer, and put the Jira tracking issue in the PR title rather
than the commit message.

Preserve the traceability fields and keep the replacement and retirement together
in the final commit after squashing or restructuring. When the replacement
already exists, the retirement commit records where that responsibility resides.

### Completion and Progress

A transfer is complete when its replacement and platform-specific v1 retirement
are committed together and accepted with the review record above. Identification,
partial implementation, or a replacement left running alongside the selected v1
execution does not complete the transfer.

Accepted commits provide the historical record. Track remaining v1 execution by
platform and record intentional removals separately from ports. These observations
describe migration work remaining; they do not imply a percentage of semantic
equivalence. No permanent inventory of semantic identities is required.
