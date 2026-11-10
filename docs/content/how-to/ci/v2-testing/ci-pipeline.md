# CI Pipeline Configuration

Use this page when adding or changing CI jobs. For test authoring and selection
in existing jobs, follow [Writing V2 Tests](writing-tests.md). For execution and
process boundaries, see the [execution model](index.md#execution-model).

## Repository ownership

[openshift/hypershift](https://github.com/openshift/hypershift) owns the tests,
platform configuration, lifecycle orchestration, and
[test image definition](https://github.com/openshift/hypershift/blob/main/Dockerfile.e2e).
[openshift/release](https://github.com/openshift/release) owns the CI jobs and step
registry that consume that image.

Use the current source for implementation details:

- [Lifecycle commands](https://github.com/openshift/hypershift/tree/main/test/e2e/v2/cmd):
  creation, test execution, diagnostic collection, and teardown.
- [Platform configuration](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/lifecycle/platform.go)
  and [test plans](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/lifecycle/testplan.go):
  supported interfaces and plan validation.
- [Platform implementations](https://github.com/openshift/hypershift/tree/main/test/e2e/v2/lifecycle):
  current variants and coverage allocation.
- [HyperShift step registry](https://github.com/openshift/release/tree/master/ci-operator/step-registry/hypershift)
  and [job configurations](https://github.com/openshift/release/tree/master/ci-operator/config/openshift/hypershift):
  workflows, credentials, images, timeouts, and job selection.

For registry conventions and workflow, chain, and ref definitions, follow the
[OpenShift CI step registry documentation](https://docs.ci.openshift.org/docs/architecture/step-registry/).

## Lifecycle handoff

Creation records cluster identities and teardown inputs in a
[shared manifest](https://github.com/openshift/hypershift/blob/main/test/e2e/v2/lifecycle/manifest.go)
before provisioning. Execution, collection, and teardown consume that manifest,
allowing cleanup after partial provisioning. Preserve this handoff when changing
orchestration, and ensure the CI workflow schedules diagnostic collection and
teardown after failures.

## Add or change a job

1. Identify whether the change concerns test selection or CI infrastructure.
   Follow the [authoring guide](writing-tests.md#supply-prerequisites-and-ci-selection)
   for labels, cluster prerequisites, and plans. Adding coverage to an existing
   selection does not require a release repository change.
2. If platform or framework work is needed, agree on the approach through the
   [advance discussion procedure](writing-tests.md#discuss-framework-changes-first),
   then implement it in HyperShift using the current interfaces above.
3. In the release repository, reuse an appropriate workflow and its existing
   steps where possible. Add or update registry components when the job needs
   different infrastructure. Ensure the steps receive the required images,
   credentials, and configuration. For Azure diagnostic access, see
   [worker diagnostic prerequisites](../../azure/troubleshooting/debug-nodes.md#collect-worker-diagnostics).
4. Add or update the job under `ci-operator/config/openshift/hypershift/`.
   Review triggers, change filters, image dependencies, and timeouts against the
   intended coverage. Use current neighboring jobs as examples instead of copying
   an old configuration fragment.
5. From the release repository root, run `make jobs WHAT=openshift/hypershift`
   and the applicable repository validation. Review generated changes and submit
   the release PR with references to any corresponding HyperShift changes.

Validate that the resulting job selects and exercises the intended coverage;
use [Verify and debug](writing-tests.md#verify-and-debug) to inspect its results.
