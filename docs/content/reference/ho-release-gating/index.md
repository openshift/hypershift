# HyperShift Operator Konflux Release Gating

The HyperShift Operator (HO) uses a Konflux-based release gating pipeline to validate nightly builds before promoting them to downstream consumers. A nightly CronJob selects the latest Snapshot, triggers Prow-hosted E2E tests against it, and only promotes the image when all blocking tests pass.

## How It Works

Every night, a CronJob triggers a new HO build in Konflux. Once the build completes and a Snapshot is created, the Integration Service evaluates an IntegrationTestScenario (ITS) that launches the release gating pipeline. The pipeline:

1. Extracts the HO container image from the Snapshot
2. Triggers blocking and informing E2E tests via Gangway (Prow CI)
3. Evaluates the test results against the gate criteria
4. Creates a Release CR if the gate passes, which triggers image promotion
5. Sends a Slack notification with the outcome
6. Checks for stale promotion (consecutive days of gate failures) and sends a dedicated alert if the threshold is exceeded

## Documentation Pages

| Page | Description |
|------|-------------|
| [Release Strategy](strategy.md) | Why release gating exists, blocking vs informing tests, gate verdict logic, stale promotion alerting |
| [Architecture](architecture.md) | End-to-end flow, RBAC, Tekton pipeline internals, integration points |
| [Adding E2E Tests](extending-tests.md) | How to add, remove, or reclassify E2E tests in the gate |
| [Extending to Other Services](extending-services.md) | How to set up release gating for a new managed service |
| [Operations and Troubleshooting](troubleshooting.md) | Manual triggers, inspecting runs, common failure scenarios |
