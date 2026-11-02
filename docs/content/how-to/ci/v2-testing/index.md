# V2 E2E Test Framework

The v2 framework uses Ginkgo to test hosted clusters, with individual spec results
for CI reporting and Component Readiness.

- [Writing V2 Tests](writing-tests.md): Authoring invariants, test selection,
  local verification, and debugging.
- [Migrating from V1 to V2](migration.md): Procedure for transferring test
  responsibility and retiring the corresponding v1 execution.
- [Test Flow](test-flow.md): Execution flow, process boundaries, and test isolation.
- [CI Pipeline Configuration](ci-pipeline.md): Job infrastructure and integration
  with the openshift/release step registry.
