---
name: Konflux Archived PipelineRuns
description: "Accesses archived Konflux PipelineRuns, TaskRuns, and pod logs via KubeArchive. Auto-applies when checking Konflux PipelineRun results, investigating enterprise contract failures, or retrieving logs from completed Konflux CI runs."
---

# Konflux Archived PipelineRun Access

This skill provides the workflow for accessing Konflux PipelineRuns that have been archived by the kube archiver. PipelineRuns are archived quickly after completion and are typically NOT available via `oc get`. Use the KubeArchive REST API to retrieve PipelineRun details, TaskRun results, and pod logs.

## When to Use This Skill

This skill automatically applies when:
- Checking results of any completed Konflux PipelineRun
- Investigating Konflux enterprise contract check failures
- Retrieving logs from finished Konflux CI builds or tests
- Analyzing trusted task violations in CI
- Looking at Konflux check results on GitHub PRs
- A PipelineRun is not found via `oc get` in the Konflux namespace

## Architecture

### Konflux CI on HyperShift

- **Namespace:** `crt-redhat-acm-tenant`
- **Cluster:** `api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443`
- PipelineRuns are archived quickly by kube archiver and are typically NOT available via `oc get`

### KubeArchive

Archived PipelineRuns, TaskRuns, pods, and pod logs are accessible through the KubeArchive REST API:

```
KA_HOST="https://kubearchive-api-server-product-kubearchive.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com"
```

Authentication uses the `oc` token:
```bash
curl -s -H "Authorization: Bearer $(oc whoami -t)" "${KA_HOST}/livez"
```

## Accessing Archived Resources

### Fetch an Archived PipelineRun

```bash
curl -s -H "Authorization: Bearer $(oc whoami -t)" \
  "${KA_HOST}/apis/tekton.dev/v1/namespaces/crt-redhat-acm-tenant/pipelineruns/<PIPELINERUN_NAME>"
```

Child TaskRun references are in `status.childReferences`:
```python
data['status']['childReferences']  # list of {name, kind, apiVersion, pipelineTaskName}
```

### Fetch an Archived TaskRun

```bash
curl -s -H "Authorization: Bearer $(oc whoami -t)" \
  "${KA_HOST}/apis/tekton.dev/v1/namespaces/crt-redhat-acm-tenant/taskruns/<TASKRUN_NAME>"
```

TaskRun results are in `status.results`.

### Find Pods for a TaskRun

```bash
curl -s -H "Authorization: Bearer $(oc whoami -t)" \
  "${KA_HOST}/api/v1/namespaces/crt-redhat-acm-tenant/pods?labelSelector=tekton.dev/taskRun=<TASKRUN_NAME>"
```

### Fetch Pod Logs

List available containers first from the pod spec (`spec.initContainers` and `spec.containers`), then fetch logs:

```bash
curl -s -H "Authorization: Bearer $(oc whoami -t)" \
  "${KA_HOST}/api/v1/namespaces/crt-redhat-acm-tenant/pods/<POD_NAME>/log?container=<CONTAINER_NAME>"
```

## Enterprise Contract Violations

### Identifying Failing EC Checks from GitHub

```bash
HEAD_SHA=$(gh pr view <PR> --repo openshift/hypershift --json headRefOid -q .headRefOid)

# Find failing EC check runs
gh api repos/openshift/hypershift/commits/${HEAD_SHA}/check-runs --paginate \
  --jq '.check_runs[] | select(.name | test("enterprise-contract")) | select(.conclusion == "failure") | {name: .name, id: .id}'

# Get PipelineRun names from check output
gh api repos/openshift/hypershift/commits/${HEAD_SHA}/check-runs --paginate \
  --jq '.check_runs[] | select(.name | test("enterprise-contract")) | select(.conclusion == "failure") | .output.text'
```

The PipelineRun name appears in an `<a href="...">` tag in the output text.

### EC Verify Task Pod Containers

The EC verify task pod has these containers with useful output:
- **`step-report-json`** - Structured JSON with all violations (preferred)
- **`step-summary`** - Human-readable summary
- **`step-detailed-report`** - Detailed report

### EC JSON Report Structure

```json
{
  "success": false,
  "components": [{
    "name": "component-name",
    "containerImage": "quay.io/...",
    "violations": [{
      "msg": "Human-readable message",
      "metadata": {
        "code": "rule.code.name",
        "title": "Rule title",
        "description": "Rule description",
        "solution": "How to fix"
      }
    }]
  }]
}
```

Group violations by `metadata.code` and present a summary with counts, rule names, and individual messages.

### Common EC Violation Types

#### `tasks.required_untrusted_task_found`
A required task is present but not resolved from a trusted version. Fix by updating the task reference in `.tekton/` pipeline files.

#### `trusted_task.trusted`
A task version is not in the trusted task list. The violation message includes the required SHA to upgrade to. Fix by updating task digests in `.tekton/` pipeline files.

## Error Handling

- **`oc whoami -t` fails:** User must log in to the Konflux cluster with `oc login`
- **KubeArchive `/livez` fails:** Check that `oc` is logged in to the correct cluster (`api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443`)
- **KubeArchive returns 404 for a resource:** May not be archived yet; try `oc get` directly in namespace `crt-redhat-acm-tenant`
- **Pod logs return "no logs found":** Logs may have been purged; fall back to TaskRun results for the summary
- **No failing EC checks found:** Report that all EC checks passed
