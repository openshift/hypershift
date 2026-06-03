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

4. **Open the PR in draft mode**: This prevents automatic CI job execution and avoids notifying the approver before the PR has been reviewed. Once your PR is in draft mode:

   - **Run necessary CI jobs manually**: See the [CI Test Pipeline](#ci-test-pipeline) section below for details on how tests are organized and how to trigger them.

   - **Request reviews**: Use `/auto-cc` to assign reviewers

   - **Assign an approver**: Use `/assign @approver` to assign an approver. Usually the openshift-ci assistant will suggest an approver based on the changes and the OWNERS file.

   Only mark the PR as "Ready for Review" once all required tests are passing and required labels are applied:

   **Required labels to merge:**
   - `approved` - From an approver via `/approve`
   - `lgtm` - From a reviewer via `/lgtm`
   - `jira/valid-reference` - PR title contains a valid Jira ticket reference (or NO-JIRA in case no associated issue)
       **DISCLAIMER:** NO-JIRA should be use sparingly. Please have a Jira issue associated with your PR whenever possible. 
   - `area/*` - Area label (e.g., `area/documentation`, `area/control-plane`)
   - `verified` - QA verification passed (for more information refer to the [documentation](https://docs.ci.openshift.org/docs/architecture/jira/#the-verified-label))

   **Conditionally required:**
   - `jira/valid-bug` - Required for bug fixes
   - `backport-risk-assessed` - Required for backports

5. **Explain the value**: Always describe how your change improves the project in the PR description.

> **📝 Note: Release Information**
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

## CI Test Pipeline

HyperShift uses a **two-stage CI test pipeline** to manage costs. E2e tests run on multi-tier nested clusters (management cluster → hosted cluster) which are resource-intensive, so they do not run automatically on every PR update.

### Stage 1: Automatic (cheap/fast)

These jobs run automatically when a PR is opened or updated (unless in draft mode):

- Unit tests
- Verify/lint checks
- Other lightweight validation

### Stage 2: On-demand (expensive e2e)

The expensive end-to-end tests are configured with `always_run: false` and `optional: true` in Prow. They run in one of two ways:

1. **Automatically after `lgtm`**: When a reviewer applies the `lgtm` label (via `/lgtm`), all required second-stage e2e tests are triggered automatically.
2. **Manually via `/test` commands**: You can trigger individual or all second-stage tests at any time using Prow comments on the PR.

### Triggering Tests Manually

Use the following `/test` commands as PR comments to trigger specific test suites:

| Command | Description |
|---------|-------------|
| `/test <job-name>` | Run a specific e2e test (e.g., `/test e2e-aws`) |
| `/test remaining-required` | Trigger all remaining required tests that have not yet run |
| `/test all` | Run all CI jobs (both stages) |
| `/retest` | Re-run all failed tests |

#### Common e2e test names (main branch)

The exact list of available tests is defined in the [Prow presubmit configuration](https://github.com/openshift/release/tree/master/ci-operator/jobs/openshift/hypershift). Common examples include:

- `/test e2e-aws` — AWS e2e tests
- `/test e2e-aks` — Azure AKS e2e tests
- `/test e2e-aws-ovn-serial` — AWS serial e2e tests
- `/test e2e-aws-ovn-proxy` — AWS proxy e2e tests
- `/test e2e-aws-conformance` — AWS conformance tests
- `/test e2e-aws-techpreview` — AWS TechPreview e2e tests

> **💡 Tip: Save CI resources**
>
> If you only need to validate behavior on a specific platform (e.g., Azure), trigger just that platform's tests instead of all of them. For example, use `/test e2e-aks` rather than `/test all`.

### Important notes

- **If you manually trigger any stage-2 test before `lgtm`**, the `lgtm` label will not automatically re-trigger those tests. Use `/test remaining-required` to trigger any tests that still need to run.
- **You can trigger e2e tests before `lgtm`** if you need early evidence that your changes work (e.g., to validate a fix or gather results for a reviewer).
- **Draft PRs do not run any tests automatically**. You must use `/test` commands to run tests while in draft mode.
- The authoritative list of available test jobs and their names is maintained in the [`openshift/release`](https://github.com/openshift/release/tree/master/ci-operator/jobs/openshift/hypershift) repository.
