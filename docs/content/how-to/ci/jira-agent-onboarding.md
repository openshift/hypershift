# Jira Agent Onboarding Guide

This guide walks you through onboarding your project to the Jira Agent — an automated system that processes Jira issues, implements fixes using Claude Code, and creates draft PRs.

!!! info "Generic Step Registry"
    The Jira Agent is implemented as a generic, parameterized step registry in
    `openshift/release` at `ci-operator/step-registry/jira-agent/`. Any team can
    reuse it by creating a thin wrapper workflow with their own env vars and credentials.

## Overview

The Jira Agent runs as a periodic OpenShift CI (Prow) job. Each run:

1. Queries Jira for issues matching your JQL filter
2. Clones your fork repository
3. For each issue, runs a 4-phase pipeline:
   - **Solve** — Claude Code analyzes the issue and implements a fix
   - **Review** — Claude Code reviews the changes for code quality
   - **Fix** — Claude Code addresses review findings
   - **PR Creation** — Creates a draft PR against the upstream repo
4. Updates Jira (adds labels, transitions status, sets assignee)
5. Sends a Slack notification with results
6. Generates an HTML report with token usage and cost breakdown

## Prerequisites

Before onboarding, you need:

### 1. GitHub App

A GitHub App installed on both your **fork org** and **upstream org** with these permissions:

- Repository: Contents (read/write), Pull Requests (read/write), Issues (read)
- The app needs separate installation IDs for fork and upstream

### 2. Fork Repository

A fork of your upstream repo where the agent pushes branches:

- Example: `my-org/my-repo` as a fork of `openshift/my-repo`
- The agent clones the fork, creates branches, and pushes changes
- PRs are created from `fork-org:branch` → `upstream-org:main`

### 3. Vault Secret

A secret in the `test-credentials` namespace containing:

| Key | Description |
|-----|-------------|
| `claude-prow` | GCP service account JSON for Vertex AI authentication |
| `jira-token` | Jira API token (for Basic auth) |
| `jira-user` | Jira username/email |
| `github-app-id` | GitHub App ID |
| `github-app-private-key` | GitHub App private key (PEM format) |
| `installation-id` | GitHub App installation ID for the **fork** org |
| `<upstream-install-id-key>` | GitHub App installation ID for the **upstream** org (key name is configurable) |
| `slack-webhook` | Slack incoming webhook URL |

Request a Vault secret from the CI team. See [OpenShift CI Secret Management](https://docs.ci.openshift.org/docs/how-tos/adding-a-new-secret-to-ci/).

### 4. Jira Configuration

Set up your Jira project to work with the agent:

- Create a label `issue-for-agent` — the agent queries for issues with this label
- The agent adds `agent-processed` after processing to avoid re-processing
- Ensure issues have a **Context** section and **Acceptance criteria** in the description

### 5. Claude Code Plugins

The agent uses the `jira-solve` command from ai-helpers and optionally the `code-review` plugin. If your project needs additional plugins (e.g., language-specific linters), list them in `JIRA_AGENT_EXTRA_PLUGIN_COMMANDS`.

## Setup Steps

### Step 1: Create Your Wrapper Workflow

Create a workflow YAML in the `openshift/release` step registry under your team's directory. This workflow references the generic `jira-agent` steps and sets your team-specific configuration.

**Example:** `ci-operator/step-registry/my-team/jira-agent/my-team-jira-agent-workflow.yaml`

```yaml
workflow:
  as: my-team-jira-agent
  steps:
    pre:
      - ref: jira-agent-setup
    test:
      - ref: jira-agent-process
    post:
      - ref: jira-agent-report
  env:
    JIRA_AGENT_FORK_REPO: "my-org/my-repo"
    JIRA_AGENT_UPSTREAM_REPO: "openshift/my-repo"
    JIRA_AGENT_JQL: >-
      project = MYPROJ AND resolution = Unresolved
      AND status in (New, "To Do")
      AND labels = issue-for-agent
      AND labels != agent-processed
  documentation: |-
    My Team's Jira Agent wrapper workflow.
```

!!! warning "Credentials"
    The generic step refs currently use `hypershift-team-claude-prow` as the credential
    secret name. If your team uses a different secret, you'll need to create your own
    ref YAMLs that point to the generic command scripts but with your credential name.
    See [Credential Override](#credential-override) below.

### Step 2: Create the Periodic Job

Add a periodic test entry in your project's CI config under `ci-operator/config/`:

```yaml
- as: periodic-jira-agent
  cron: "30 8 * * 1"    # Weekly on Monday at 8:30 UTC
  steps:
    env:
      JIRA_AGENT_MAX_ISSUES: "3"
    workflow: my-team-jira-agent
```

### Step 3: Generate and Validate

```bash
cd ~/path-to/release
make update                    # Regenerate job configs
make validate-step-registry    # Validate step registry
make checkconfig               # Validate Prow config
```

### Step 4: Submit PR

Submit a PR to `openshift/release` with your new workflow and periodic job configuration.

## Configuration Reference

### Required Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `JIRA_AGENT_FORK_REPO` | Fork repo (`org/repo`) | `"my-org/my-repo"` |
| `JIRA_AGENT_UPSTREAM_REPO` | Upstream repo (`org/repo`) | `"openshift/my-repo"` |
| `JIRA_AGENT_JQL` | JQL query for issue discovery | `'project = MYPROJ AND labels = issue-for-agent'` |

### Optional Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `JIRA_AGENT_TARGET_STATUS` | `""` | JSON map of project prefix to target status after processing. Example: `'{"MYPROJ":"In Progress"}'` |
| `JIRA_AGENT_ASSIGNEE` | `""` | Display name for auto-assigning processed issues |
| `JIRA_AGENT_UPSTREAM_INSTALLATION_ID_KEY` | `o-h-installation-id` | Key name in Vault secret for upstream GH App installation ID |
| `JIRA_AGENT_FORK_INSTALLATION_ID_KEY` | `installation-id` | Key name in Vault secret for fork GH App installation ID |
| `JIRA_AGENT_EXTRA_PLUGIN_COMMANDS` | `""` | Newline-separated Claude plugin install commands |
| `JIRA_AGENT_TOOL_SETUP_SCRIPT` | `""` | Shell commands to install project-specific tools |
| `JIRA_AGENT_REVIEW_LANGUAGE` | `go` | Language for the code-review plugin |
| `JIRA_AGENT_REVIEW_PROFILE` | `""` | Profile name for the code-review plugin |
| `JIRA_AGENT_SLACK_EMOJI` | `:robot:` | Slack emoji in notifications |
| `JIRA_AGENT_MAX_ISSUES` | `1` | Max issues per run |
| `JIRA_AGENT_ISSUE_KEY` | `""` | Override to process a specific issue (skips JQL) |
| `CLAUDE_MODEL` | `claude-opus-4-6` | Claude model to use |
| `JIRA_BASE_URL` | `https://redhat.atlassian.net` | Jira instance base URL |

### Triggering for a Specific Issue

You can trigger the job for a specific Jira issue via the Gangway API:

```json
{
  "pod_spec_options": {
    "envs": {
      "MULTISTAGE_PARAM_OVERRIDE_JIRA_AGENT_ISSUE_KEY": "MYPROJ-123"
    }
  }
}
```

## Credential Override

The generic `jira-agent` step refs declare a credential secret name. If your team needs a different secret:

1. Create thin ref YAMLs in your team's step registry directory that reference the generic commands but with your credential:

    ```yaml
    # ci-operator/step-registry/my-team/jira-agent/setup/my-team-jira-agent-setup-ref.yaml
    ref:
      as: my-team-jira-agent-setup
      from: claude-ai-helpers
      commands: jira-agent-setup-commands.sh
      credentials:
      - namespace: test-credentials
        name: my-team-claude-prow          # Your team's secret
        mount_path: /var/run/claude-code-service-account
      resources:
        requests:
          cpu: 100m
          memory: 200Mi
    ```

2. Do the same for the process ref (include all env vars from the generic ref or inherit them via the workflow `env` block).

3. Update your workflow to reference your team's refs instead of the generic ones.

## Jira Issue Format

For best results, structure Jira issue descriptions with:

### Required Sections

- **Context** — Background information about the problem
- **Acceptance criteria** — Clear criteria for what the fix should accomplish

### Optional Sections

- **Steps to reproduce** — For bugs, numbered reproduction steps
- **Expected vs actual behavior** — What should happen vs what happens

### Example Issue Description

```markdown
## Context
The FooController does not handle the case where the bar field is nil,
causing a nil pointer dereference when reconciling resources created
before v4.15.

## Acceptance Criteria
- The controller handles nil bar field gracefully
- Existing resources without the bar field continue to work
- Unit tests cover the nil case

## Steps to Reproduce
1. Create a Foo resource without the bar field
2. Wait for reconciliation
3. Observe panic in controller logs
```

## JQL Examples

```sql
-- Simple: all issues with agent label
project = MYPROJ AND labels = issue-for-agent AND labels != agent-processed

-- With status filter
project = MYPROJ AND resolution = Unresolved AND status in (New, "To Do")
  AND labels = issue-for-agent AND labels != agent-processed

-- Multiple projects
project in (MYPROJ, OTHERPROJ) AND resolution = Unresolved
  AND status in (New, "To Do") AND labels = issue-for-agent
  AND labels != agent-processed

-- With priority filter
project = MYPROJ AND priority in (High, Highest)
  AND labels = issue-for-agent AND labels != agent-processed
```

## Troubleshooting

### Job fails with "Claude Code CLI not found"

The `claude-ai-helpers` image may not have Claude Code installed. Verify the image is up to date.

### Job fails with authentication errors

Check that your Vault secret contains valid credentials:

- `claude-prow` — Valid GCP service account JSON with Vertex AI API access
- `jira-token` / `jira-user` — Valid Jira credentials with issue read/write permissions
- `github-app-private-key` — Valid PEM key for your GitHub App

### No issues found

- Verify your JQL query returns results in the Jira UI
- Check that issues have the `issue-for-agent` label
- Check that issues do NOT have the `agent-processed` label
- Ensure the Jira user has permission to view the project

### PR creation fails

- Verify the GitHub App has Contents and Pull Requests write permissions on both fork and upstream
- Check that installation IDs in the secret match the correct org installations
- Ensure the fork is synced with upstream (the agent does this automatically, but check for force-push protections)

### Slack notification not sent

- Verify `slack-webhook` in the Vault secret is a valid incoming webhook URL
- Check job logs for webhook response errors

## Reference Implementation

See the HyperShift team's implementation for a complete working example:

- **Wrapper workflow**: [`ci-operator/step-registry/hypershift/jira-agent/hypershift-jira-agent-workflow.yaml`](https://github.com/openshift/release/tree/main/ci-operator/step-registry/hypershift/jira-agent)
- **Periodic job config**: [`ci-operator/config/openshift/hypershift/openshift-hypershift-main.yaml`](https://github.com/openshift/release/blob/main/ci-operator/config/openshift/hypershift/openshift-hypershift-main.yaml)
- **Generic steps**: [`ci-operator/step-registry/jira-agent/`](https://github.com/openshift/release/tree/main/ci-operator/step-registry/jira-agent)
- **Existing HyperShift docs**: [AI-Assisted CI Jobs](ai-assisted-ci-jobs.md)
