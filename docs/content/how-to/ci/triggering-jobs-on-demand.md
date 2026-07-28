# :zap: Triggering CI Jobs On Demand

The HyperShift AI-assisted periodic jobs run on a schedule, but you can trigger them on demand via the [Gangway API](https://docs.ci.openshift.org/docs/how-tos/gangway/) to process specific issues or run jobs immediately.

---

## :wrench: Setup

### :package: Install the OpenShift Developer Plugin Bundle

Install the **openshift-developer** plugin bundle for Claude Code to get the CI, Jira, and code-review skills used by the HyperShift agentic workflows.

=== ":zap: Quick Install"

    ```bash
    # Add the marketplaces (one-time)
    claude plugin marketplace add openshift-eng/ai-helpers
    claude plugin marketplace add RedHatProductSecurity/prodsec-skills

    # Install the bundle
    claude plugin install openshift-developer@ai-helpers
    ```

=== ":package: What's Included"

    The `openshift-developer` bundle installs these plugins:

    | Plugin | Purpose |
    |--------|---------|
    | `ci` | OpenShift CI / Prow job analysis, gangway triggers (`/ci:trigger-periodic`, `/ci:query-job-status`) |
    | `jira` | Jira automation (`/jira:solve`, `/jira:ready-to-solve`) |
    | `code-review` | Pre-commit and PR review (`/code-review:pr`, `/code-review:pre-commit-review`) |
    | `golang` | Go development tools (gopls LSP, gofmt, golangci-lint) |
    | `git` | Git workflow automation and conventional commit formatting |
    | `prodsec-skills` | Product security guidance |

!!! tip ":bulb: Non-Claude Code editors"
    The bundle can also be installed via APM for other editors:

    ```bash
    apm install openshift-eng/ai-helpers/plugins/openshift-developer --global --target cursor
    ```

### :key: Authenticate to the CI Cluster

You need to be logged in to the `app.ci` cluster to use the Gangway API.

1. Visit the [app.ci console](https://console-openshift-console.apps.ci.l2s4.p1.openshiftapps.com/)
2. Log in with your Red Hat SSO credentials
3. Click your username :material-arrow-right: **Copy login command**
4. Paste and run the `oc login` command in your terminal

Verify with:

```bash
oc whoami
```

---

## :robot: Jira Agent

**Full job name**: `periodic-ci-openshift-hypershift-main-periodic-jira-agent`

### :dart: Trigger for a Specific Issue

=== ":robot: Claude Code"

    ```
    /ci:trigger-periodic periodic-ci-openshift-hypershift-main-periodic-jira-agent \
      MULTISTAGE_PARAM_OVERRIDE_JIRA_AGENT_ISSUE_KEY=CNTRLPLANE-1234
    ```

=== ":octicons-terminal-16: curl"

    ```bash
    curl -s -X POST \
      -H "Authorization: Bearer $(oc whoami -t)" \
      -H "Content-Type: application/json" \
      -d '{
        "job_name": "periodic-ci-openshift-hypershift-main-periodic-jira-agent",
        "job_execution_type": "1",
        "pod_spec_options": {
          "envs": {
            "MULTISTAGE_PARAM_OVERRIDE_JIRA_AGENT_ISSUE_KEY": "CNTRLPLANE-1234"
          }
        }
      }' \
      https://gangway-ci.apps.ci.l2s4.p1.openshiftapps.com/v1/executions
    ```

    Replace `CNTRLPLANE-1234` with the Jira issue key you want the agent to process.

!!! info ":gear: How the override works"
    Setting `MULTISTAGE_PARAM_OVERRIDE_JIRA_AGENT_ISSUE_KEY` overrides the
    `JIRA_AGENT_ISSUE_KEY` parameter in the multistage job. This tells the agent
    to skip its normal JQL query and process only the specified issue with
    `key = <issue-key>`.

### :arrows_counterclockwise: Trigger with Default JQL

Run the agent with its normal JQL query (picks up labeled, unprocessed issues):

=== ":robot: Claude Code"

    ```
    /ci:trigger-periodic periodic-ci-openshift-hypershift-main-periodic-jira-agent
    ```

=== ":octicons-terminal-16: curl"

    ```bash
    curl -s -X POST \
      -H "Authorization: Bearer $(oc whoami -t)" \
      -H "Content-Type: application/json" \
      -d '{
        "job_name": "periodic-ci-openshift-hypershift-main-periodic-jira-agent",
        "job_execution_type": "1"
      }' \
      https://gangway-ci.apps.ci.l2s4.p1.openshiftapps.com/v1/executions
    ```

---

## :mag: Checking Job Status

The Gangway API response includes an execution ID. Use it to check the job status:

=== ":robot: Claude Code"

    ```
    /ci:query-job-status <execution-id>
    ```

=== ":octicons-terminal-16: curl"

    ```bash
    curl -s -X GET \
      -H "Authorization: Bearer $(oc whoami -t)" \
      https://gangway-ci.apps.ci.l2s4.p1.openshiftapps.com/v1/executions/<execution-id>
    ```

Or find the run in the [Prow job history :octicons-link-external-16:](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-hypershift-main-periodic-jira-agent).

---

## :link: Related

- [AI-Assisted CI Jobs](ai-assisted-ci-jobs.md) — overview of all HyperShift AI-assisted jobs
- [Jira Agent Onboarding Guide](jira-agent-onboarding.md) — set up the jira-agent for your team
- [Agentic SDLC](../agentic-sdlc.md) — the full agentic development workflow
- [Gangway documentation :octicons-link-external-16:](https://docs.ci.openshift.org/docs/how-tos/gangway/) — upstream Gangway API docs
