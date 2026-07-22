# Adding or Modifying E2E Tests

This page describes how to add, remove, or reclassify E2E tests for a specific managed service gate. Each managed service has its own IntegrationTestScenario (ITS) with independent test lists, so changes to one service gate do not affect the others.

## Where the Job Lists Live

The E2E test lists are defined as parameters in the ITS resource. Each ITS specifies two JSON arrays:

- `e2e-blocking-job-names`: tests that must pass for the gate to succeed
- `e2e-informing-job-names`: tests that are reported but do not block promotion

These parameters are injected into the pipeline at runtime by the Integration Service.

The ITS resources are managed via GitOps in the [Konflux Release Data](https://gitlab.cee.redhat.com/releng/konflux-release-data) repository on GitLab CEE, under:

```
tenants-config/cluster/stone-prd-rh01/tenants/crt-redhat-acm-tenant/
  hypershift-operator/nightly-promotion/its.yaml
```

Each managed service has its own ITS resource in this file, following a consistent naming convention (`hypershift-ho-release-gate-<service>`).

### ITS Structure

The ITS file contains one resource per managed service. Each ITS references the same pipeline but with different parameters:

```yaml
apiVersion: appstudio.redhat.com/v1beta2
kind: IntegrationTestScenario
metadata:
  name: hypershift-ho-release-gate-aro-hcp       # per-service name
  namespace: crt-redhat-acm-tenant
spec:
  application: hypershift-operator
  contexts:
    - name: disabled                              # CronJob-triggered only
  resolverRef:
    resolver: git
    params:
      - name: url
        value: https://github.com/openshift/hypershift.git
      - name: revision
        value: main
      - name: pathInRepo
        value: .tekton/pipelines/ho-release-gate-run.yaml
  params:
    - name: e2e-blocking-job-names                # <-- edit these lists
      value: '["periodic-ci-...-e2e-aks",
               "periodic-ci-...-e2e-aks-upgrade-minor"]'
    - name: e2e-informing-job-names
      value: '["periodic-ci-...-e2e-aks-ovn-conformance"]'
    - name: gate-label
      value: "ARO HCP"
    - name: release-plan-name
      value: "hypershift-operator-ho-release-gate-aro-hcp"
    - name: stale-threshold-days                    # <-- optional, default 3
      value: "3"
```

- `stale-threshold-days` controls how many consecutive days of gate failures must occur before a stale promotion alert is sent. The default is `3`. Adjust per service if needed (e.g. a critical service may use `2`, while a less critical one may tolerate `5`). See [Stale Promotion Alerting](strategy.md#stale-promotion-alerting).

When a new managed service is added, a second ITS resource with the same structure is appended to this file, with its own service-specific job lists, gate label, release plan name, and stale threshold.

## Adding a New Job

To add a new Prow periodic job to a service gate:

1. Ensure the job exists as a Prow periodic in the [openshift/release](https://github.com/openshift/release) repository
2. Decide whether the job should be **blocking** or **informing**
3. Edit the ITS resource for the target service and add the full job name to the appropriate JSON array parameter

## Moving a Job Between Categories

To promote a job from informing to blocking (or demote from blocking to informing):

1. Remove the job name from the source array
2. Add it to the target array
3. Submit the change as an MR to the [Konflux Release Data](https://gitlab.cee.redhat.com/releng/konflux-release-data) repository

## Job Naming Convention

The pipeline expects full Prow periodic job names following the standard OpenShift CI naming convention:

```
periodic-ci-<org>-<repo>-<branch>-<variant>-<test-name>
```

For example:

```
periodic-ci-openshift-hypershift-release-4.19-periodics-e2e-aks
```

The pipeline automatically strips the common prefix (`periodic-ci-openshift-hypershift-release-4.NN-periodics-`) when displaying results in logs and Slack notifications for readability.

## Verifying the Change

After modifying the test lists:

1. Submit the MR to the [Konflux Release Data](https://gitlab.cee.redhat.com/releng/konflux-release-data) repository
2. Wait for the next nightly run, or trigger a manual run (see [Operations and Troubleshooting](troubleshooting.md))
3. Check the Slack notification to verify the new job appears in the results
4. Inspect the PipelineRun logs to confirm the job was triggered and polled correctly
