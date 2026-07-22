# Release Strategy and Rationale

## Why Release Gating Exists

The HyperShift Operator is a core component for multiple managed OpenShift services (ARO HCP, ROSA, GCP). A broken operator image reaching production can cause widespread cluster provisioning and management failures.

Release gating adds a validation step between the Konflux build and the downstream promotion: every nightly Snapshot must pass a defined set of E2E tests before the image is promoted to the staging registry. This ensures that only validated images reach managed service environments.

<!-- TODO: add OCPSTRAT link when available -->

## Blocking vs Informing Tests

The pipeline supports two categories of E2E tests:

| Category | Semantics | Effect on Gate |
|----------|-----------|----------------|
| **Blocking** | Must pass for promotion | Gate fails if any blocking test fails |
| **Informing** | Advisory, monitored for trends | Reported in Slack but does not block promotion |

This distinction allows the team to monitor new or experimental tests without risking promotion stability. A test typically starts as informing and graduates to blocking once it has proven stable.

## Gate Verdict Logic

The gate evaluates results as follows:

- **Pass**: all blocking tests passed (informing results are reported but ignored for the verdict)
- **Fail**: one or more blocking tests failed

## What Happens When the Gate Passes

1. The pipeline creates a **Release CR** referencing the validated Snapshot and the corresponding ReleasePlan
2. The Konflux **Release Service** picks up the Release CR and triggers a **managed pipeline**
3. The managed pipeline promotes the HO image to the Quay staging repository
4. A **Slack notification** is sent with the pass verdict, test results summary, and links to the PipelineRun

## What Happens When the Gate Fails

1. **No Release CR** is created, so no promotion occurs
2. The `create-release` task exits with a non-zero code, marking the PipelineRun as failed
3. A **Slack notification** is sent with the failure verdict, identifying which blocking tests failed and including Prow job links for investigation

## Stale Promotion Alerting

A single nightly gate failure is normal and gets fixed quickly. However, if the gate keeps failing for multiple consecutive days, the last successfully promoted image becomes increasingly stale. This can go unnoticed because each individual failure notification looks the same as any other.

Stale promotion alerting solves this by tracking the history of PipelineRun outcomes per managed service and sending a dedicated alert when the number of consecutive failure days reaches a configurable threshold.

### How It Works

When the gate fails, both `notify-slack` and `notify-slack-error` perform the following steps before sending the failure notification:

1. Query the [KubeArchive](architecture.md#kubearchive) REST API for archived PipelineRuns matching the current ITS label selector
2. Walk the history from most recent to oldest, counting consecutive failures (a "failure streak")
3. If the streak spans a number of days equal to or greater than the `stale-threshold-days` parameter, send a stale promotion alert instead of the standard failure notification

The stale alert replaces the normal failure notification. It includes the streak duration in days, a list of recent failed PipelineRuns with dates, failure reasons, and links, and the current threshold value. If there is no streak or the streak is below the threshold, a standard failure notification is sent.

### Per-Service Independence

The stale check is performed independently for each managed service. The pipeline uses the ITS name as a Kubernetes label selector when querying KubeArchive, so each service's PipelineRun history is isolated. This means:

- ARO HCP and ROSA (or any future service) each have their own failure streak, tracked automatically
- A failure streak in one service does not affect or trigger alerts for another
- No additional configuration is needed beyond adding the `stale-threshold-days` parameter to the ITS

### Configuration

The stale threshold is configured per service via the `stale-threshold-days` parameter in the IntegrationTestScenario. The default value is `3` (alert after 3 consecutive days of failures). Each service can set its own threshold based on its tolerance for stale images.

See [Adding or Modifying E2E Tests](extending-tests.md) and [Extending to Other Services](extending-services.md) for how to configure this parameter in the ITS.
