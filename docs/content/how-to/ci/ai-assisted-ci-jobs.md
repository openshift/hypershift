# AI-Assisted CI Jobs

This document describes the AI-assisted CI jobs that help automate issue resolution and PR review handling in the HyperShift repository.

!!! warning "Human Review Required"
    **AI-generated code must not be relied upon without human review.** All PRs created by these jobs are drafts and must go through the standard GitHub PR review process. HyperShift repository OWNERS are responsible for reviewing and approving all changes.

!!! info "Responsible Use"
    Please review the [Guidelines on Responsible Use of AI Code Assistants](https://source.redhat.com/projects_and_programs/ai/wiki/code_assistants_guidelines_for_responsible_use_of_ai_code_assistants) before using these tools.

## Overview

HyperShift uses AI-assisted CI jobs powered by Claude Code to help with development workflows:

| Job | Purpose | Schedule |
|-----|---------|----------|
| `periodic-jira-agent` | Analyzes Jira issues and creates draft PRs with fixes | Weekly on Mondays at 8:30 AM UTC |
| `periodic-review-agent` | Addresses PR review comments on agent-created PRs | Every 3 hours (8:00-23:00 UTC) daily |
| `address-review-comments` | On-demand job to address review comments on a single PR | Triggered via `/test address-review-comments` |
| `periodic-hypershift-dependabot-triage` | Consolidates open dependabot PRs into a single weekly PR | Weekly on Fridays at 12:00 UTC |

### Usage Scope

These jobs process **internal Red Hat tickets only** from the following Jira projects:

- **OCPBUGS** - OpenShift bug tracking
- **CNTRLPLANE** - HyperShift/Control Plane team issues

---

## Jira Agent

### Overview

The Jira Agent (`periodic-jira-agent`) automatically analyzes Jira issues and creates draft pull requests with proposed fixes.

- **Job name**: `periodic-jira-agent`
- **Schedule**: Weekly on Mondays at 8:30 AM UTC (`30 8 * * 1`)
- **Max issues per run**: 1 (configurable via `JIRA_AGENT_MAX_ISSUES`)
- **Max agentic turns**: 100 per issue

### How It Works

1. **Queries Jira** for unresolved issues matching the criteria (see JQL below)
2. **Clones repositories**: ai-helpers and hypershift-community/hypershift fork
3. **Runs Claude Code** with the `/jira-solve` command to analyze each issue and implement a fix
4. **Creates draft PR** from the `hypershift-community/hypershift` fork to `openshift/hypershift`
5. **Updates Jira** after successful processing:
   - Adds `agent-processed` label
   - Transitions to "ASSIGNED" (OCPBUGS) or "Code Review" (CNTRLPLANE)
   - Sets assignee to `hypershift-automation`

### JQL Query

Issues are selected for processing using this query:

```sql
project in (OCPBUGS, CNTRLPLANE)
  AND resolution = Unresolved
  AND status in (New, "To Do")
  AND labels = issue-for-agent
  AND labels != agent-processed
```

### Data Flow

```mermaid
flowchart TD
    subgraph "Prow CI Environment"
        A[Periodic Job Trigger<br/>Weekly Monday 8:30 UTC] --> B[Setup Step]
        B --> C[Process Step]

        subgraph "Process Step"
            C --> D[Query Jira API]
            D --> E{Issues Found?}
            E -->|No| F[Exit Successfully]
            E -->|Yes| G[Clone Repositories]
            G --> H[For Each Issue]
            H --> I[Run Claude Code<br/>/jira-solve command]
            I --> J[Create Branch]
            J --> K[Push to Fork]
            K --> L[Create Draft PR]
            L --> M[Update Jira Labels]
            M --> N{More Issues?}
            N -->|Yes| H
            N -->|No| F
        end
    end

    subgraph "External Systems"
        D <--> JIRA[(Jira API<br/>issues.redhat.com)]
        I <--> CLAUDE[Claude API<br/>via Vertex AI]
        K <--> FORK[(GitHub Fork<br/>hypershift-community)]
        L <--> UPSTREAM[(GitHub Upstream<br/>openshift/hypershift)]
    end
```

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `JIRA_AGENT_MAX_ISSUES` | 1 | Maximum issues to process per run |
| Rate limit | 60 seconds | Delay between processing issues |

---

## Review Agent

### Overview

The Review Agent (`periodic-review-agent`) automatically addresses PR review comments on PRs created by the Jira Agent.

- **Job name**: `periodic-review-agent`
- **Schedule**: Every 3 hours (8:00-23:00 UTC) daily (`0 8-23/3 * * *`)
- **Max PRs per run**: 10 (configurable via `REVIEW_AGENT_MAX_PRS`)
- **Max agentic turns**: 100 per PR
- **On-demand job**: `address-review-comments` (trigger with `/test address-review-comments`)

### How It Works

1. **Queries GitHub** for open PRs authored by `app/hypershift-jira-solve-ci`
2. **Analyzes review threads** to identify comments needing attention
3. **Runs Claude Code** with the `/utils:address-reviews` command
4. **Pushes changes** back to the PR branch

### Comment Analysis Logic

The agent uses a Python-based comment analyzer to intelligently determine which review threads need attention. This prevents duplicate responses and ensures only actionable feedback is processed.

#### What Gets Processed

| Condition | Action |
|-----------|--------|
| No bot reply in thread | Process (first response needed) |
| Human replied after bot's last comment | Process (follow-up needed) |
| Bot already replied, no human follow-up | Skip (already addressed) |
| Thread is resolved | Skip (marked complete by reviewer) |
| Thread is outdated (code changed) | Skip (likely addressed by code change) |

#### What Counts as an Unresolved Review Thread

A review thread is considered **unresolved** when:

- **Inline code comments**: A reviewer left a comment on a specific line of code in the "Files changed" tab, and no one has clicked "Resolve conversation"
- **Review comments with suggestions**: Comments that include suggested code changes that haven't been resolved
- **Threaded discussions**: Any reply chain started from a code review that remains open

A review thread is **NOT** created by:

- General PR comments (comments in the main "Conversation" tab that aren't attached to code)
- PR reviews that only contain an approval/request changes without inline comments
- Commit comments

#### Author Authorization

The review agent only responds to feedback from authorized authors:

| Author Type | Example |
|-------------|---------|
| OpenShift org members | Members of the `openshift` GitHub organization |
| OWNERS file entries | Users listed in `OWNERS` or `OWNERS_ALIASES` |
| Approved bots | `coderabbitai[bot]` |

Comments from unauthorized users are ignored to prevent abuse.

#### Response Rules

When addressing feedback, the bot follows these rules:

1. **One response per feedback**: Never responds to the same feedback via both inline reply AND general PR comment
2. **Code changes only when requested**: Only modifies code when explicitly asked (imperative language like "change", "fix", "update")
3. **Explanations for questions**: Replies with explanation only for clarifying questions, without code changes

### Data Flow

```mermaid
flowchart TD
    subgraph "Prow CI Environment"
        A[Periodic Job Trigger<br/>Every 3 hours 8:00-23:00 UTC] --> B[Setup Step]
        B --> C[Process Step]

        subgraph "Process Step"
            C --> D[Query GitHub API<br/>for Agent PRs]
            D --> E{PRs Found?}
            E -->|No| F[Exit Successfully]
            E -->|Yes| G[For Each PR]
            G --> H[Analyze Review Threads]
            H --> I{Threads Need<br/>Attention?}
            I -->|No| J{More PRs?}
            I -->|Yes| K[Checkout PR Branch]
            K --> L[Run Claude Code<br/>/utils:address-reviews]
            L --> M[Push Changes]
            M --> J
            J -->|Yes| G
            J -->|No| F
        end
    end

    subgraph "External Systems"
        D <--> GH[(GitHub API<br/>github.com)]
        L <--> CLAUDE[Claude API<br/>via Vertex AI]
        M <--> FORK[(GitHub Fork<br/>hypershift-community)]
    end
```

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `REVIEW_AGENT_MAX_PRS` | 10 | Maximum PRs to process per run |

---

## Dependabot Triage Agent

### Overview

The Dependabot Triage Agent (`periodic-hypershift-dependabot-triage`) automatically consolidates open dependabot PRs into a single weekly pull request, reducing noise and simplifying dependency updates.

- **Job name**: `periodic-hypershift-dependabot-triage`
- **Schedule**: Weekly on Fridays at 12:00 UTC (`0 12 * * 5`)
- **Process timeout**: 2 hours
- **Max agentic turns**: 100
- **Jira**: [CNTRLPLANE-2588](https://issues.redhat.com/browse/CNTRLPLANE-2588)
- **Prow config PR**: [openshift/release#73790](https://github.com/openshift/release/pull/73790)

### How It Works

1. **Setup**: Verifies Claude Code CLI availability
2. **PR Discovery**: Queries all open dependabot PRs via `gh pr list` on the `openshift/hypershift` repository
3. **Filtering**: Excludes PRs that bump `k8s.io` or `sigs.k8s.io` dependencies (these are managed manually as part of coordinated Kubernetes rebases)
4. **Processing**: Invokes Claude Code to process each PR individually:
      - Cherry-picks commits onto a consolidation branch
      - Runs `make verify` and `make test` after each PR
      - Resets and skips any PR that fails validation
5. **Commit Reorganization** (deterministic bash, not LLM): Flattens all cherry-pick commits via `git reset` and reorganizes into logical groups:
      1. Root `go.mod`/`go.sum`
      2. Root `vendor/`
      3. `api/go.mod`/`api/go.sum`
      4. `api/vendor/`
      5. `hack/tools/go.mod`/`hack/tools/go.sum`
      6. `hack/tools/vendor/`
      7. Regenerated CRD assets (`cmd/install/assets/`)
      8. Remaining generated files
      Empty groups are skipped automatically.
6. **Final Validation**: Runs two-pass `make verify` and `make test` on the consolidated branch
7. **Output**: Creates a single consolidated PR from `hypershift-community:fix/weekly-dependabot-consolidation` to `openshift/hypershift`
8. **Reporting**: Generates an HTML report with token usage, cost breakdown, and detailed output

### Data Flow

```mermaid
flowchart TD
    subgraph "Prow CI Environment"
        A[Periodic Job Trigger<br/>Weekly Friday 12:00 UTC] --> B[Setup Step]
        B --> C[Process Step]
        C --> D[Report Step]

        subgraph "Process Step"
            C --> E[Generate GitHub App Tokens]
            E --> F[Clone Fork<br/>hypershift-community/hypershift]
            F --> G[Query Open Dependabot PRs]
            G --> H{PRs Found?}
            H -->|No| I[Exit Successfully]
            H -->|Yes| J[Filter Out k8s.io Bumps]
            J --> K[For Each PR]
            K --> L[Cherry-pick + Validate<br/>make verify & make test]
            L --> M{Passed?}
            M -->|Yes| N[Keep on Branch]
            M -->|No| O[Reset & Skip]
            N --> P{More PRs?}
            O --> P
            P -->|Yes| K
            P -->|No| Q[Reorganize Commits<br/>Deterministic Bash]
            Q --> R[Final Validation<br/>make verify & make test]
            R --> S[Create PR]
        end
    end

    subgraph "External Systems"
        G <--> GH[(GitHub API<br/>github.com)]
        L <--> CLAUDE[Claude API<br/>via Vertex AI]
        S <--> FORK[(GitHub Fork<br/>hypershift-community)]
        S <--> UPSTREAM[(GitHub Upstream<br/>openshift/hypershift)]
    end
```

### Configuration

| Setting | Value | Description |
|---------|-------|-------------|
| Schedule | `0 12 * * 5` | Fridays at 12:00 UTC (7:00 AM ET) |
| Process timeout | 2 hours | Maximum time for the process step |
| Max Claude turns | 100 | Maximum agentic turns per run |
| Excluded deps | `k8s.io`, `sigs.k8s.io` | Dependencies managed via manual Kubernetes rebases |

### What Gets Excluded

Dependabot PRs bumping the following module prefixes are **automatically skipped**:

- `k8s.io/*` - Core Kubernetes libraries
- `sigs.k8s.io/*` - Kubernetes SIG libraries

These dependencies are updated manually as part of coordinated Kubernetes version rebases to ensure compatibility across the full dependency tree.

---

## User Guide

### Submitting Issues for Processing

To have an issue processed by the Jira Agent:

1. Ensure the issue is in **OCPBUGS** or **CNTRLPLANE** project
2. Set status to **New** or **To Do**
3. Ensure resolution is **Unresolved**
4. Add the label **`issue-for-agent`**
5. Security set to none

The issue will be picked up on the next weekly run (Mondays at 8:30 AM UTC).

### Viewing AI-Generated Output

Track PRs created by the AI agents:

- **Jira Agent PRs**: [github.com/openshift/hypershift/pulls?q=is:pr+author:app/hypershift-jira-solve-ci](https://github.com/openshift/hypershift/pulls?q=is%3Apr+author%3Aapp%2Fhypershift-jira-solve-ci)
- **Dependabot Triage PRs**: [github.com/openshift/hypershift/pulls?q=is:pr+head:fix/weekly-dependabot-consolidation](https://github.com/openshift/hypershift/pulls?q=is%3Apr+head%3Afix%2Fweekly-dependabot-consolidation)

PRs are created as **drafts** and require human review before merging.

### Reprocessing Issues

To have an issue reprocessed:

1. Remove the **`agent-processed`** label from the Jira issue
2. The issue will be picked up on the next weekly run

### Triggering Review Agent On-Demand

For a single PR, you can trigger the review agent manually:

```
/test address-review-comments
```

This runs the review agent for that specific PR only.

---

## Limitations

- **AI may produce incorrect or incomplete solutions** - always review carefully
- **Complex issues may not be fully addressed** - multi-faceted problems may need human intervention
- **Rate limited**: 1 issue per weekly run (jira-agent), 10 PRs per run (review-agent), all non-k8s dependabot PRs per run (dependabot-triage)
- **Cannot access private resources** - no access to internal systems beyond Jira/GitHub
- **Cannot execute destructive operations** - no ability to delete resources or force-push
- **Maximum agentic turns**: 100 per issue (jira-agent), 100 per PR (review-agent)

---

## Support and Feedback

- **Slack channel**: #project-hypershift
- **Feedback**: File issues in [openshift/hypershift](https://github.com/openshift/hypershift/issues) with label `ai-feedback`
- **Urgent issues**: Contact HyperShift OWNERS directly

---

## Monitoring and Effectiveness

### Performance Monitoring

- **Prow job logs**: [prow.ci.openshift.org](https://prow.ci.openshift.org/?repo=openshift%2Fhypershift)
- Track job success/failure rates
- Monitor for recurring authentication errors

### Metrics and Indicators

| Metric | Description |
|--------|-------------|
| Issues processed per week | Number of issues successfully analyzed |
| PRs merged vs. closed | Success rate of generated PRs |
| Review cycles needed | Average iterations before merge |
| Time to PR creation | Duration from issue creation to PR |

### Periodic Review Process

The HyperShift team conducts monthly reviews:

- Review AI-generated PRs for quality and accuracy
- Track false positives and missed solutions
- Adjust issue labeling criteria based on results
- Document lessons learned and improve the `/jira-solve` command

---

## Data Flow and Security

### Authentication

| System | Method |
|--------|--------|
| GitHub | GitHub App tokens (JWT-based) for fork and upstream |
| Claude API | GCP service account via Vertex AI |
| Jira | Personal access token for label management |

### Data Retention

- No persistent storage beyond PR content
- Logs retained per Prow standard retention policy

### External Systems

| System | Purpose |
|--------|---------|
| Jira API (issues.redhat.com) | Issue queries and label updates |
| GitHub API (github.com) | PR creation and management |
| Claude API via Vertex AI (GCP) | AI-powered code analysis and generation |
