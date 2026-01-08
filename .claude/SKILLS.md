# HyperShift Claude Skills

This directory contains Claude Code skills that are automatically applied when working on the HyperShift project.

## Available Skills

### Code Formatting

**Location:** `.claude/skills/code-formatting/`

**Description:** Applies HyperShift code quality, formatting and testing conventions.

**Auto-applies when:**
- Writing Go code
- Creating unit tests
- Preparing commits

**Covers:**
- Running `make lint-fix` after writing Go code
- Running `make verify` before committing
- Using `make verify-codespell` for markdown
- "When...it should..." test naming convention
- Including unit tests with new functions

**Benefits:**
- Ensures code passes linting before commits
- Enforces consistent test naming
- Catches spelling errors in documentation

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

## How Skills Work

Skills are automatically invoked by Claude based on context. You don't need to do anything special - just ask Claude to:
- Write or review Go code → Code Formatting + Effective Go apply automatically
- Create commits → Git Commit Format applies automatically
- Create tests → Code Formatting ("When...it should..." convention) applies
- Debug hosted-cluster issues → Debug Cluster applies automatically

## Available Commands

Commands are manually invoked using `/command-name` syntax.

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

### Update Konflux Tasks

**Location:** `.claude/commands/update-konflux-tasks.md`

**Description:** Automatically update outdated Konflux Tekton tasks based on enterprise contract verification logs.

**Usage:**
```
/update-konflux-tasks <path-to-log-file>
```

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
