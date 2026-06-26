# Konflux CI Tools

Tools for working with Konflux pipelines and builds. Several tasks are
available both as Claude Code skills (automated) and as standalone scripts
(manual). The standalone scripts live under `hack/tools/` and require `oc`
access to the Konflux cluster (`api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443`)
in namespace `crt-redhat-acm-tenant`.

## Predict which pipelines a PR triggers

You've changed a Containerfile and some Go code, and want to know which
Konflux pipelines will fire before you push.

**Tool:** `hack/tools/check-konflux-triggers/`

Evaluates Pipelines-as-Code CEL expressions from `.tekton/*pull-request*`
files against the current branch's changed files. Uses the same `gobwas/glob`
and `google/cel-go` libraries as Pipelines as Code.

```bash
# Check which pipelines would trigger for your current changes:
(cd hack/tools && go run ./check-konflux-triggers/)

# Compare against a different base (e.g., your upstream remote):
(cd hack/tools && go run ./check-konflux-triggers/ upstream/main)
```

## Build an image from a PR

**Scenario 1:** You opened a PR with an OCPBUGS fix and need the built
image to deploy in a staging cluster for manual validation — but the PR
hasn't merged yet.

**Scenario 2:** A colleague asks you to test their PR, but it was opened
a week ago and the 5-day image expiry has already garbage-collected it
from the registry. You need a fresh build with a longer TTL.

**Skill:** `/konflux-build` — creates a manual Konflux PipelineRun from a
PR with configurable image expiry (default 30 days).

```
# In Claude Code:
/konflux-build 8761
/konflux-build https://github.com/openshift/hypershift/pull/8761
```

## Investigate Enterprise Contract failures

Your PR shows a red "enterprise-contract" check on GitHub. The check output
says something about untrusted tasks or policy violations, but the details
are sparse. The PipelineRun has already been archived and `oc get` returns
nothing.

**Skill:** `/konflux-ec-violations` — accesses archived PipelineRuns via
the KubeArchive REST API, retrieves the EC verify task's JSON report, and
summarises violations grouped by rule.

```
# In Claude Code, pass the PR number or URL:
/konflux-ec-violations 8761
```

The skill fetches the archived PipelineRun from KubeArchive, finds the
EC verify task pod, reads the `step-report-json` container logs, and
presents the violations with rule codes, descriptions, and suggested fixes.

## Update outdated Tekton tasks

Enterprise Contract checks are failing with `trusted_task.trusted` — your
`.tekton/` pipeline files reference task bundle digests that are no longer
in the trusted list.

**Skill:** `/update-konflux-tasks` — parses EC violation logs, identifies
outdated tasks, fetches migration notes from the
[build-definitions](https://github.com/konflux-ci/build-definitions) repo,
and applies the digest updates.

```
# In Claude Code:
/update-konflux-tasks
```

### Standalone scripts

The skill uses two scripts that can also be run directly:

**`hack/tools/scripts/update_trusted_task_bundles.py`** — fetches the
trusted tasks data from the Konflux `data-acceptable-bundles` OCI artifact
and updates pipeline YAML files to the latest trusted digests.

```bash
# Update all .tekton/ pipeline files at once:
hack/tools/scripts/update_trusted_task_bundles.py

# Update only the operator push pipeline:
hack/tools/scripts/update_trusted_task_bundles.py .tekton/hypershift-operator-main-push.yaml
```

Prerequisites: `python3`, `oras`

**`hack/tools/scripts/find_task_version_by_digest.sh`** — maps a task
container image digest to its semantic version tag. Useful when an EC
violation message gives you a digest like `sha256:a7cc...` and you need
to know "is this version 0.8 or 0.9?".

```bash
# Find which version of clair-scan corresponds to a digest:
hack/tools/scripts/find_task_version_by_digest.sh clair-scan \
  sha256:a7cc183967f89c4ac100d04ab8f81e54733beee60a0528208107c9a22d3c43af
```

Prerequisites: `skopeo`, `jq`

## Test tag pipeline changes

You're modifying the tag pipeline definition in `.tekton/` (e.g., adding a
new build step or changing the multi-arch platforms). You need to verify
the changes actually work before merging — but tag pipelines only trigger
on `git tag` pushes, so normal PR CI won't exercise them.

**Skill:** `/test-tag-pipeline` — creates a manual PipelineRun from a tag
commit using your branch's pipeline definition, waits for it to complete,
and optionally triggers EC validation via a Snapshot.

```
# In Claude Code, from your branch with tag pipeline changes:
/test-tag-pipeline v0.1.69
```

### Standalone scripts

The skill orchestrates two scripts that can be used independently:

**`hack/tools/scripts/create-manual-tag-pipelinerun.sh`** — creates the
manual PipelineRun by patching the tag pipeline YAML with your branch's
commit SHA and submitting it.

```bash
# Test the tag pipeline from main against tag v0.1.69:
hack/tools/scripts/create-manual-tag-pipelinerun.sh v0.1.69

# Test using the pipeline definition from your fork branch:
hack/tools/scripts/create-manual-tag-pipelinerun.sh v0.1.69 celebdor:fix-tag-pipeline

# Then watch the PipelineRun until it finishes:
oc get pipelinerun -n crt-redhat-acm-tenant -w -l pipelinesascode.tekton.dev/event-type=push
```

Prerequisites: `oc`, `yq`

**`hack/tools/scripts/create-snapshot-from-pipelinerun.sh`** — after the
build completes, creates an `appstudio.redhat.com/v1alpha1` Snapshot
resource to trigger Enterprise Contract (EC) validation on the resulting
image. The Snapshot links the built container image back to its source
commit and target branch, which is exactly the metadata EC needs to run
its policy checks.

The script has two modes:

1. **From a live PipelineRun** — extracts `IMAGE_URL`, `IMAGE_DIGEST`,
   commit SHA, and target branch directly from the PipelineRun's
   `status.results` and annotations.
2. **Direct image parameters** — useful when the PipelineRun has already
   been garbage-collected. You provide the image coordinates and commit
   metadata explicitly.

```bash
# Mode 1: From a completed PipelineRun (extracts image details automatically):
hack/tools/scripts/create-snapshot-from-pipelinerun.sh \
  hypershift-operator-main-manual-v0.1.69-abc12

# Mode 2: Direct parameters (e.g., after PipelineRun was garbage-collected):
hack/tools/scripts/create-snapshot-from-pipelinerun.sh \
  --image-url quay.io/redhat-user-workloads/crt-redhat-acm-tenant/hypershift-operator-main:v0.1.69 \
  --image-digest sha256:016e754d... \
  --pipelinerun hypershift-operator-main-manual-v0.1.69-gb49w \
  --commit 6e6ecadc61361e4fe359af34dcdee17df06c664e \
  --target-branch refs/tags/v0.1.69

# Monitor the resulting EC IntegrationTestScenario run:
oc get snapshot <snapshot-name> -n crt-redhat-acm-tenant -w
oc get pipelinerun -n crt-redhat-acm-tenant -l appstudio.openshift.io/snapshot=<snapshot-name>
```

Prerequisites: `oc`, `jq`
