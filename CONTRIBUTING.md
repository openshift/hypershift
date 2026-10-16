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

> **Tip: Install pre-commit hooks**
>
> Install `pre-commit` to automatically catch issues before committing. This helps catch spelling mistakes, formatting issues, and test failures early in your development process.
>
> * [Installation instructions](https://pre-commit.com/#install)
> * [HyperShift-specific tips](docs/content/contribute/precommit-hook-help.md)

## Creating a Pull Request
1. **For small changes** (under 200 lines): Create your change and submit a pull request directly.

2. **For larger changes** (200+ lines): Get feedback on your approach first by opening a GitHub issue or posting in the #project-hypershift Slack channel. This prevents situations where large changes get declined after significant work.

3. **Write a clear PR title**: Prefix with your Jira ticket number (e.g., "OCPBUGS-12345: Fix memory leak in controller"). See [example PR](https://github.com/openshift/hypershift/pull/2233).

4. **Explain the value**: Always describe how your change improves the project in the PR description.

### CI

This project uses [Prow](https://docs.ci.openshift.org/) and [GitHub Actions](https://github.com/openshift/hypershift/actions) for CI. Lightweight checks (linting, unit tests, verification) run automatically on pushes and pull requests. E2E tests that consume real cloud infrastructure only run after a reviewer grants `/lgtm`, to avoid unnecessary resource usage on work-in-progress PRs.

**Adding CI targets:** When you add a new `make` target to `verify-parallel` or any other CI-facing Makefile target, you must also add it to the corresponding GitHub Actions workflow (e.g., `.github/workflows/verify-reusable.yaml` for verify targets, or as a new "reusable" target). GitHub Actions provide fast feedback on PRs; Prow runs heavier e2e tests. Targets that only exist in the Makefile without a matching GH Actions step will not run in CI.

See hack/github-actions-runner/README.md for details.

Useful Prow commands:

- `/test <job-name>` - Run a specific CI job
- `/test all` - Run all CI jobs
- `/retest` - Re-run failed CI jobs
- `/retest-required` - Re-run failed required CI jobs
- `/pipeline required` - Trigger all second-phase CI jobs (e2e tests gated behind `/lgtm`)
- `/label tide/merge-method-squash` - Squash commits on merge (useful for PRs with multiple related commits)

#### Retest and override comment format

When you ask CI to re-run or bypass a job, say **why** in the comment so reviewers and agents can audit the decision. Both `/retest` and `/override` comments follow the same shape:

1. The command on the first line. For `/override`, the job identifier MUST be the GitHub status-context form `ci/prow/<test-name>` (quoted), **not** the full `pull-ci-openshift-hypershift-<branch>-<test-name>` prow job name — the latter is rejected with "unknown contexts/checkruns were given."
2. A blank line.
3. A reason line that starts with a **reason keyword in ALL CAPS**, followed by any required tracking bug and an explanation.

Example (from [#9961](https://github.com/openshift/hypershift/pull/9961#issuecomment-6214025296)):

```
/override "ci/prow/e2e-v2-azure-self-managed"

FLAKE OCPBUGS-132050 — This job failed on unrelated NodePool tests (OSImageStream
"config hash annotation" empty, plus trust-bundle / MachineConfig-replace rollouts
hitting `context deadline exceeded`), which is the known Azure self-managed flake.
This PR only touches backup-restore e2e test files, and the backup-restore steps
passed in the same run, so the failure is not related to this change.
```

The reason keyword is open ended, but prefer one of these well-understood ones:

- **UNRELATED** — The change touches files that are not under test in any way or shape. This is a high bar: it is **not** enough to believe the change "shouldn't have" broken the test. Reserve it for cases where the files you touched are not even part of the images used by the test.
- **FLAKE** — A known intermittent failure. Must be accompanied by a tracking bug ticket to be valid, plus an explanation of why the failure you are overriding is the **same** failure described in that ticket.
- **INFRA** — The job ran while the underlying infrastructure was down. Must be accompanied by a tracking bug ticket showing the infra outage. Use this very rarely — it is meant for fast-tracking a fix while infra is broken.

### Required labels to merge

- `approved` - From an approver via `/approve`
- `lgtm` - From a reviewer via `/lgtm`
- `jira/valid-reference` - PR title contains a valid Jira ticket reference (or NO-JIRA if no associated issue)
    **Note:** NO-JIRA should be used sparingly. Please have a Jira issue associated with your PR whenever possible.
- `area/*` - Area label (e.g., `area/documentation`, `area/control-plane`)
- `verified` - QA verification passed (for more information refer to the [documentation](https://docs.ci.openshift.org/docs/architecture/jira/#the-verified-label))

**Conditionally required:**
- `jira/valid-bug` - Required for bug fixes
- `backport-risk-assessed` - Required for backports

> **Note: Release Information**
>
> This repository contains code for both the HyperShift Operator and Control Plane Operator (part of OCP payload), which may have different release cadences.

## Review Process

### Managing Reviewers and Approvers

After using `/auto-cc` to assign reviewers and `/assign` for approvers, you have complete autonomy to adjust these assignments as needed. You are responsible for ensuring your PR has appropriate reviewers and an approver.

Once reviewers and approvers are correctly assigned, they become responsible for the review and approval process:

- **Reviewers** are expected to review the PR or hand it over to another reviewer if unable to do so (using `/un-cc` themselves and `/cc` a replacement)
- **Approvers** are expected to eventually approve the PR (using `/approve`) or reassign to another approver if unable (using `/unassign` themselves and `/assign` a replacement)
- **Using holds**: Both reviewers and approvers can use `/lgtm` or `/approve` together with `/hold` to transparently set a blocking condition for merging while already approving. For example: `/hold until open discussion is resolved`. Anyone can `/unhold` when ready.

Use these Prow commands to manage assignments:

- **Adjusting reviewers**: Use `/un-cc @reviewer` to remove unsuitable reviewers, then run `/auto-cc` again for new suggestions
- **Adjusting approvers**: Use `/unassign @approver` to remove and `/assign @approver` to add approvers

**Common scenarios:**

1. **Auto-assigned reviewers are not suitable**:
   - Use `/un-cc @reviewer` to remove them
   - Run `/auto-cc` again for different suggestions

2. **Approver was also selected as reviewer**:
   - Use `/un-cc @approver` to remove the approver from reviewers
   - Run `/auto-cc` again to get additional reviewers

3. **Need to change the approver**:
   - Use `/unassign @current-approver`
   - Use `/assign @new-approver`

Remember: If the openshift-ci assistant assigns unsuitable reviewers or approvers, don't hesitate to adjust the assignments. Being proactive helps ensure your PR gets timely and appropriate review.

## Agentic Software Development

The HyperShift team operates an Agentic Software Development Life Cycle (ASDLC) that integrates AI agents into the development workflow. Agents can assist with issue analysis, implementation, code review, and CI triage using reusable skills and knowledge bases.

See [docs/content/how-to/agentic-sdlc.md](docs/content/how-to/agentic-sdlc.md) for the full framework, available building blocks, and how to use them in your workflow.
