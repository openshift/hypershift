# Operations and Troubleshooting

## Manual Trigger Strategies

There are two ways to manually trigger the release gating pipeline, each with different scope.

### Full CronJob Run (All Service Gates)

This re-runs the entire nightly flow, triggering all managed service gates defined in `ITS_NAMES`:

```bash
oc create job --from=cronjob/hypershift-operator-nightly-promotion \
  ho-release-gate-manual-$(date +%s) -n crt-redhat-acm-tenant
```

Use this when you need to re-validate all services (e.g. after a shared infrastructure fix).

### Snapshot Label (Single Service Gate)

This triggers only one specific ITS, useful for re-testing a single service without affecting others:

```bash
SNAPSHOT_NAME=$(oc get snapshot -n crt-redhat-acm-tenant \
  --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[-1].metadata.name}')

oc label snapshot "$SNAPSHOT_NAME" \
  test.appstudio.openshift.io/scenario=<its-name> \
  -n crt-redhat-acm-tenant --overwrite
```

Replace `<its-name>` with the target ITS name, for example `hypershift-ho-release-gate-aro-hcp`. The Integration Service will detect the label and create a new PipelineRun for that ITS only.

### When to Use Which

| Scenario | Strategy |
|----------|----------|
| Re-validate all services after an infrastructure change | CronJob |
| Re-test a single service after fixing a service-specific issue | Snapshot label |
| Test a new ITS configuration | Snapshot label |
| Nightly run failed due to a transient error | Snapshot label (for the affected gate) |

## Inspecting a PipelineRun

### Fetching the PipelineRun

List recent release gating PipelineRuns:

```bash
oc get pipelineruns -n crt-redhat-acm-tenant \
  --sort-by=.metadata.creationTimestamp | tail -5
```

### Reading Task Logs

Find the pods for a specific PipelineRun, then read the logs for a specific task step:

```bash
oc get pods -n crt-redhat-acm-tenant -l tekton.dev/pipelineRun=<pipelinerun-name>

oc logs pod/<pod-name> -c step-<step-name> -n crt-redhat-acm-tenant
```

!!! warning

    PipelineRun pods are subject to aggressive garbage collection on the Konflux cluster. If the pods have already been deleted, use the Konflux UI instead (see below), where logs are persisted, centralized, and aggregated across all tasks of the pipeline.

### Konflux UI

PipelineRun logs and Release CR status are available in the [Konflux PipelineRuns view](https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com/ns/crt-redhat-acm-tenant/applications/hypershift-operator/activity/pipelineruns).

!!! tip

    Use the PipelineRun name from the `oc get pipelineruns` command above to filter the list in the UI.

### Historical PipelineRun Data (KubeArchive)

The `oc get pipelineruns` command only returns PipelineRuns that still exist on the cluster. Due to aggressive garbage collection on stone-prd-rh01, PipelineRuns are deleted shortly after completion. For historical data (e.g. investigating a stale promotion streak or reviewing failures beyond what the Slack notification displays), query the KubeArchive REST API directly:

```bash
curl -s -H "Authorization: Bearer $(oc whoami -t)" \
  "https://kubearchive-api-server-product-kubearchive.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com/apis/tekton.dev/v1/namespaces/crt-redhat-acm-tenant/pipelineruns?labelSelector=test.appstudio.openshift.io/scenario=<ITS_NAME>" \
  | python3 -c "
import json,sys
data=json.load(sys.stdin)
for item in data.get('items',[]):
    name=item['metadata']['name']
    conds=item.get('status',{}).get('conditions',[])
    if conds:
        c=conds[-1]
        print(f\"{name:50s} status={c.get('status','?'):6s} reason={c.get('reason','?')}\")
"
```

!!! note

    The KubeArchive URL in the curl command above corresponds to the default value of the `kubearchive-api-base` pipeline parameter. If the pipeline has been reconfigured to point at a different KubeArchive instance, use that URL instead.

Replace `<ITS_NAME>` with the target service gate name (e.g. `hypershift-ho-release-gate-aro-hcp`). You must be logged in to the stone-prd-rh01 cluster (`oc login`).

The output lists all archived PipelineRuns for that ITS with their completion status and reason. To inspect a specific PipelineRun from the results, build the Konflux UI URL from its name:

```
https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com/ns/crt-redhat-acm-tenant/applications/hypershift-operator/pipelineruns/<pipelinerun-name>/
```

This URL provides the full task logs, results, and pipeline visualization even after the PipelineRun has been garbage-collected from the cluster.

## Common Failure Scenarios

### Gangway Token Expired

**Symptom**: `run-e2e` task fails with HTTP 401 errors when triggering Prow jobs.

**Fix**: rotate the `gangway-token` Secret in `crt-redhat-acm-tenant`. The token is a Prow CI cluster OAuth token.

### Slack Webhook 4xx

**Symptom**: `notify-slack` logs show repeated 4xx errors after 3 retries.

**Fix**: verify the webhook URL in the `slack-webhook` Secret is still valid. Slack webhooks can be revoked if the app is reinstalled.

### clone-lib Failure

**Symptom**: `clone-lib` task fails with git errors.

**Common causes**:

- Repository URL or branch changed
- GitHub rate limiting on unauthenticated git clones
- Network connectivity from the Konflux cluster

### PVC Issues

**Symptom**: tasks fail with workspace mount errors or permission denied on shared files.

**Common causes**:

- PVC quota exceeded in the tenant namespace
- Storage class unavailable
- Stale PVCs from previous failed runs (Konflux garbage-collects these, but delays can occur)

### Prow Job Timeout

**Symptom**: `run-e2e` task reaches its 4-hour polling timeout with jobs still pending.

**Common causes**:

- Prow cluster capacity issues (jobs queued but not scheduled)
- The E2E test itself is stuck or abnormally slow
- Gangway API returning stale status

**Mitigation**: check the Prow job directly in the Prow UI using the URL from the `run-e2e` task logs. If the job is stuck, it may need to be manually cancelled in Prow before re-triggering the gate.

### KubeArchive Unreachable

**Symptom**: `notify-slack` or `notify-slack-error` logs show warnings about failing to fetch PipelineRun history from KubeArchive. The gate result notification is still sent, but no stale promotion alert appears.

**Common causes**:

- KubeArchive service is down or restarting on stone-prd-rh01
- The projected ServiceAccount token has expired or the audience (`kubearchive`) is misconfigured
- Network policy changes blocking cluster-internal traffic

**Impact**: the stale check is a non-blocking operation. If KubeArchive is unreachable, the pipeline logs a warning and skips the stale alert. The gate verdict and notification are not affected. The stale check will resume automatically on the next run when KubeArchive becomes available again.

### Unexpected Stale Alert

**Symptom**: a stale promotion alert is sent even though the gate has not been failing for long, or the streak count seems wrong.

**Common causes**:

- `stale-threshold-days` is set too low in the ITS (e.g. `1` would alert on the first failure)
- Test PipelineRuns from integration testing contribute to the real streak history because they are archived with the same ITS label. The streak resets automatically on the first successful nightly run
- KubeArchive returned incomplete data (e.g. after a data migration or cleanup)
