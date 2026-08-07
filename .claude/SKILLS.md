# HyperShift Claude Skills

This directory contains Claude Code skills that are automatically applied when working on the HyperShift project.

## Available Skills

### Git Commit Format

**Location:** `.claude/skills/git-commit-format/`

**Description:** Applies HyperShift conventional commit formatting rules.

**Auto-applies when:**

- Generating commit messages
- Creating commits
- Discussing commit practices

**Covers:**

- Conventional commit format: `<type>(<scope>): <description>`
- All commit types (feat, fix, docs, style, refactor, test, chore, build, ci, perf, revert)
- Breaking change syntax (`!` and `BREAKING CHANGE` footer)
- `Signed-off-by` and `Assisted-by` footers
- Gitlint validation rules (120 char title, 140 char body)
- Running `make run-gitlint` for validation

**Benefits:**

- Ensures consistent commit message format
- Passes gitlint validation automatically
- Includes proper attribution footers
- Follows conventional commits specification

### Effective Go

**Location:** `.claude/skills/effective-go/`

**Description:** Automatically applies Go best practices and idioms from [golang.org/doc/effective_go](https://go.dev/doc/effective_go) when writing or reviewing Go code.

**Auto-applies when:**

- Writing new Go code
- Reviewing or refactoring existing Go code
- Debugging Go-specific issues
- Discussing Go best practices

**Covers:**

- Formatting and code style (gofmt)
- Naming conventions (packages, interfaces, functions)
- Control structures and error handling
- Concurrency patterns (goroutines, channels, select)
- Interface design principles
- Common anti-patterns to avoid

**Benefits:**

- Ensures consistent, idiomatic Go code across the project
- Catches common mistakes during development
- Promotes best practices for concurrency and error handling
- Provides quick reference during code reviews

### Konflux Archived PipelineRuns

**Location:** `.claude/skills/konflux-ec-violations/`

**Description:** Accesses archived Konflux PipelineRuns, TaskRuns, and pod logs via KubeArchive. Also analyzes enterprise contract violations.

**Auto-applies when:**

- Checking results of any completed Konflux PipelineRun
- Investigating Konflux enterprise contract check failures
- Retrieving logs from finished Konflux CI builds or tests
- A PipelineRun is not found via `oc get` in the Konflux namespace

**Covers:**

- Accessing archived PipelineRuns, TaskRuns, and pod logs via KubeArchive REST API
- Identifying failing EC checks from GitHub PR check runs
- Fetching and parsing EC violation logs from archived pods
- Grouping and presenting violations by rule code

**Benefits:**

- Retrieves details from PipelineRuns that are no longer available via `oc get`
- Provides structured violation summaries for quick triage
- Works without Konflux UI access

### Debug Cluster

**Location:** `.claude/skills/debug-cluster/`

**Description:** Provides systematic debugging approaches for HyperShift hosted-cluster issues.

**Auto-applies when:**

- Debugging hosted-cluster problems
- Investigating stuck deletions
- Troubleshooting control plane issues
- Analyzing NodePool lifecycle issues
- Reviewing operator logs for cluster problems

**Covers:**

- Hosted-cluster deletion debugging workflow
- NodePool, HostedControlPlane, and namespace cleanup
- CAPI resource troubleshooting
- Finalizer inspection and removal
- Operator log analysis (HO and CPO)
- Common issues and resolutions

**Benefits:**

- Systematic approach to debugging cluster issues
- Reduces time spent investigating stuck resources
- Provides ready-to-use kubectl commands
- Covers common scenarios and resolutions

### Create CPO Override

**Location:** `.claude/skills/create-cpo-override/`

**Description:** Interactively creates control-plane-operator image override entries in `overrides.yaml`. Automates image discovery, fix verification, YAML editing, and PR preparation.

**Auto-applies when:**

- Creating or updating CPO image overrides
- Preparing hotfix override PRs

**Covers:**

- Image resolution via `skopeo` and `oc adm release info`
- Fix verification against PR commits
- YAML editing of `hypershift-operator/controlplaneoperator-overrides/assets/overrides.yaml`
- PR preparation compatible with `/validate-pr-override-images`

**Requirements:**

- `skopeo`, `oc`, `gh` CLI installed and authenticated
- Release branches fetched locally

### Validate PR Override Images

**Location:** `.claude/skills/validate-pr-override-images/`

**Description:** Validates that CPO override images in a PR actually contain the PRs they claim to include.

**Usage:**

```
/validate-pr-override-images <PR-URL-or-number>
```

**Auto-applies when:**

- Reviewing CPO override PRs
- Verifying hotfix image contents

**Covers:**

- Parsing the PR description's structured validation contract (`branch: X wants: PR1, PR2`)
- Inspecting images via `skopeo` to verify claimed fixes are present
- Cross-referencing commits against release branches

### Triage Leaked Infrastructure

**Location:** `.claude/skills/triage-leaked-infra/`

**Description:** Assesses whether AWS VPCs or infrastructure sets from HyperShift CI are safe to delete. Every claim is backed by empirical AWS queries.

**Auto-applies when:**

- User pastes `cleanleaked` output and asks if resources are safe to delete
- Investigating orphaned AWS VPCs or infra sets
- Triaging LEAKED or UNCERTAIN verdicts

**Covers:**

- Protection tag checks (`do-not-delete`, `ci-cluster`)
- VPC-to-infraID derivation and reverse lookup
- Running instance and resource checks
- Safety verdicts (PASS/FAIL/UNKNOWN) per check

**Requirements:**

- AWS CLI configured for the HyperShift CI account (`us-east-1`)

### Development Workflows

**Location:** `.claude/skills/dev/`

**Description:** A collection of development workflow skills for building, deploying, and testing HyperShift locally.

**Sub-skills:**

| Skill | Description |
|-------|-------------|
| `build-cpo-image` | Build and push a control-plane-operator container image for live cluster testing |
| `build-ho-image` | Build and push a hypershift-operator container image for live cluster testing |
| `create-hc-aws` | Create a HostedCluster on AWS for development/testing, with optional custom CPO/HO images |
| `destroy-hc-aws` | Destroy a HostedCluster and all associated AWS infrastructure (VPC, IAM, Route53, etc.) |
| `e2e-run-aws` | Run and iterate on HyperShift e2e tests against a live cluster |
| `git-env` | Create development environments with git worktrees, branches, commits, and push to remote |
| `install-ho-aws` | Install HyperShift Operator with private AWS and external-dns settings |

**Auto-applies when:**

- Building container images for testing
- Creating or destroying hosted clusters for development
- Running e2e tests locally
- Setting up git worktrees for development

## How Skills Work

Skills are automatically invoked by Claude based on context. You don't need to do anything special - just ask Claude to:

- Write or review Go code → Effective Go applies automatically
- Create commits → Git Commit Format applies automatically
- Create tests → See [TESTING.md](../TESTING.md) for naming and placement conventions
- Debug hosted-cluster issues → Debug Cluster applies automatically

## Available Commands

Commands are manually invoked using `/command-name` syntax.

### Restructure Commits

**Location:** `.claude/commands/restructure-commits.md`

**Description:** Reorganizes all commits on a feature branch into logical, component-based commits that match HyperShift's architecture.

**Usage:**

```
/restructure-commits
```

**Use when:**

- User asks to "redo commits", "restructure commits", "squash by component", or "organize commits"
- Preparing a branch for PR review with clean commit history
- Branch has many small/WIP commits that should be consolidated

**Covers:**

- Component-based commit grouping (API, Vendor, CLI, HO, CPO, E2E, Docs)
- Soft reset and re-staging workflow
- Conventional commit messages with correct type/scope per component
- Edge cases for file categorization (support/, testdata/, API tests)

### Fix HyperShift Repo Robot PR

**Location:** `.claude/commands/fix-hypershift-repo-robot-pr.md`

**Description:** Fixes robot/bot-authored PRs that have failing CI due to missing generated files.

**Usage:**

```
/fix-hypershift-repo-robot-pr <PR-number-or-URL>
```

**What it does:**

1. Validates the PR is from a bot (`is_bot: true`)
2. Checks out the bot's PR and creates a `fix/<branch>` branch
3. Runs `make verify` to regenerate files
4. Commits any changes with conventional commit format
5. Runs `make verify` and `make test` for validation
6. If successful: Creates new PR and closes original with reference
7. If unsuccessful: Preserves original PR and reports failure

**Supported bots:**

- Dependabot (`app/dependabot`)
- Konflux (`app/red-hat-konflux`)
- Renovate (`app/renovate`)
- Any bot with `is_bot: true`

**Safety features:**

- Only processes PRs with `is_bot: true`
- Never closes original PR if validation fails
- Atomic: Original PR only closed after new PR is created

### Konflux Build

**Location:** `.claude/commands/konflux-build.md`

**Description:** Creates a manual Konflux PipelineRun that builds a container image from a PR. By default the image expires after 30 days; use `--non-expiring` for a permanent image.

**Usage:**

```
/konflux-build <PR-number-or-URL> [component-name] [--non-expiring]
```

**Examples:**

- `/konflux-build 8865 control-plane-operator` — build CPO from PR 8865 (expires in 30 days)
- `/konflux-build 7813 hypershift-release-mce-26` — build a specific component
- `/konflux-build 7813 hypershift-operator --non-expiring` — permanent image for a hotfix

**What it does:**

1. Verifies OpenShift login to the Konflux cluster (`stone-prd-rh01`)
2. Resolves the PR to get commit SHA and base branch
3. Finds the matching push pipeline template from `.tekton/` on the base branch
4. Generates a manual PipelineRun YAML with template variables resolved
5. Creates the PipelineRun and polls until completion
6. Reports the final image reference with `@sha256:` digest

**Requirements:**

- `oc` CLI logged into the Konflux cluster
- `gh` CLI authenticated with access to openshift/hypershift
- Access to the `crt-redhat-acm-tenant` namespace

### Update Konflux Tasks

**Location:** `.claude/commands/update-konflux-tasks.md`

**Description:** Automatically update outdated Konflux Tekton tasks based on enterprise contract verification logs.

**Usage:**

```
/update-konflux-tasks <path-to-log-file>
```

### Test Tag Pipeline

**Location:** `.claude/commands/test-tag-pipeline.md`

**Description:** Creates a manual PipelineRun to test tag pipeline changes before merging.

**Usage:**

```
/test-tag-pipeline <tag-name> [branch-spec]
```

**Examples:**

- `/test-tag-pipeline v0.1.69` — test main branch pipeline with a tag
- `/test-tag-pipeline v0.1.69 build-gomaxprocs-image` — test a PR branch pipeline
- `/test-tag-pipeline v0.1.69 celebdor:OCPBUGS-63194-part2` — test a fork branch pipeline

**Requirements:**

- `oc` CLI logged into the Konflux cluster

### PR Report

**Location:** `.claude/commands/pr-report.md`

**Description:** Generates comprehensive PR reports for openshift/hypershift and related repos, with optional deep code analysis, progress reports, and blog posts.

**Usage:**

```
/pr-report [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--deep] [--progress-report] [--blog]
```

**Examples:**

- `/pr-report` — last 7 days
- `/pr-report --start 2026-07-01 --end 2026-07-15 --deep` — deep analysis for a date range
- `/pr-report --start 2026-07-01 --deep --progress-report --blog` — full report with blog post

**What it does:**

1. Fetches merged PRs from openshift/hypershift, openshift-eng/ai-helpers, openshift/enhancements, and openshift/release
2. Queries Jira for ticket hierarchy (Epic, OCPSTRAT linkage)
3. Optionally analyzes code diffs and generates narrative progress reports

### E2E Analyze

**Location:** `.claude/commands/e2e-analyze.md`

**Description:** Analyzes e2e test errors from CI run artifacts, downloading logs and providing root cause analysis.

**Usage:**

```
/e2e-analyze <CI-run-URL> <test-name> <artifacts-directory>
```

**What it does:**

1. Downloads `build-log.txt` from the CI run
2. Extracts failure artifacts for the specified test
3. Fetches additional evidence from logs and events
4. Outputs structured error summary with evidence

### OCPSTRAT Review

**Location:** `.claude/commands/ocpstrat-review.md`

**Description:** Analyzes OCPSTRAT features for a component in a target OpenShift version, with optional version comparison.

**Usage:**

```
/ocpstrat-review <component> <target-version> [compare-version]
```

**Examples:**

- `/ocpstrat-review "Hosted Control Planes" 4.22`
- `/ocpstrat-review "Hosted Control Planes" 4.21 4.22` — compare across versions

### Dual-Stream Stories

**Location:** `.claude/commands/dual-stream-stories.md`

**Description:** Reviews dual-stream RHCOS epics (RHEL 9 + RHEL 10 support per NodePool) against the analysis document, creates Jira stories, and identifies gaps.

**Usage:**

```
/dual-stream-stories
```

**Context:**

- Related Jira: OCPSTRAT-3014 (Feature), CNTRLPLANE-3017/3018/3019 (Epics)
- Uses analysis from `dual-stream-analysis.html` and the enhancement proposal

## Adding New Skills

To add a new skill:

1. Create a directory: `.claude/skills/your-skill-name/`
2. Add a `SKILL.md` file with YAML frontmatter
3. Commit to the repository for team-wide availability

## Adding New Commands

To add a new command:

1. Create a file: `.claude/commands/your-command-name.md`
2. Add YAML frontmatter with `description` field
3. Document usage, process flow, and error handling
4. Commit to the repository for team-wide availability

See [Claude Code Skills Documentation](https://docs.claude.com/en/docs/claude-code/skills) for details.
