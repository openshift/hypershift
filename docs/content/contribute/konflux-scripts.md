# Konflux CI Tools

Tools for working with Konflux pipelines and builds. Several tasks are
available both as Claude Code skills (automated) and as standalone scripts
(manual). The standalone scripts live under `hack/tools/` and require `oc`
access to the Konflux cluster (`api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443`)
in namespace `crt-redhat-acm-tenant`.

## Predict which pipelines a PR triggers

Before pushing, check which Konflux pipelines your changes would trigger.

**Tool:** `hack/tools/check-konflux-triggers/`

Evaluates Pipelines-as-Code CEL expressions from `.tekton/*pull-request*`
files against the current branch's changed files. Uses the same `gobwas/glob`
and `google/cel-go` libraries as Pipelines as Code.

```bash
# From the repository root:
(cd hack/tools && go run ./check-konflux-triggers/)

# With a custom base ref (defaults to origin/main):
(cd hack/tools && go run ./check-konflux-triggers/ upstream/main)
```

## Build an image from a PR

Use this when you need a container image from an unmerged PR — for example
to test a fix in a staging cluster, or to rebuild an image whose 5-day PR
expiry has passed.

**Skill:** `/konflux-build` — creates a manual Konflux PipelineRun from a
PR with configurable image expiry (default 30 days).

## Investigate Enterprise Contract failures

When a PR has failing enterprise contract checks, use this to retrieve
the detailed violation report, pod logs, and task results from the archived
PipelineRun.

**Skill:** `/konflux-ec-violations` — accesses archived PipelineRuns via
the KubeArchive REST API, retrieves the EC verify task's JSON report, and
summarises violations grouped by rule.

## Update outdated Tekton tasks

When Enterprise Contract checks report outdated task bundles, use this to
update the `.tekton/` pipeline files to the latest trusted digests. Also
checks for migration notes and breaking changes.

**Skill:** `/update-konflux-tasks` — parses EC violation logs, identifies
outdated tasks, fetches migration notes, and applies updates.

### Standalone scripts

The skill uses two scripts that can also be run directly:

**`hack/tools/scripts/update_trusted_task_bundles.py`** — fetches the
trusted tasks data from the Konflux `data-acceptable-bundles` OCI artifact
and updates pipeline YAML files to the latest trusted digests.

```bash
# Update all .tekton/ pipeline files
hack/tools/scripts/update_trusted_task_bundles.py

# Update specific files
hack/tools/scripts/update_trusted_task_bundles.py .tekton/hypershift-operator-main-push.yaml
```

Prerequisites: `python3`, `oras`

**`hack/tools/scripts/find_task_version_by_digest.sh`** — maps a task
container image digest to its semantic version tag. Useful when EC reports
a digest and you need to know which version it corresponds to.

```bash
hack/tools/scripts/find_task_version_by_digest.sh clair-scan \
  sha256:a7cc183967f89c4ac100d04ab8f81e54733beee60a0528208107c9a22d3c43af
```

Prerequisites: `skopeo`, `jq`

## Test tag pipeline changes

When modifying the `.tekton/` tag pipeline definition, use this to validate
the changes against a real tag before merging.

**Skill:** `/test-tag-pipeline` — creates a manual PipelineRun from a tag
commit using your branch's pipeline definition, waits for it to complete,
and optionally triggers EC validation via a Snapshot.

### Standalone scripts

The skill orchestrates two scripts:

**`hack/tools/scripts/create-manual-tag-pipelinerun.sh`** — creates the
manual PipelineRun.

```bash
hack/tools/scripts/create-manual-tag-pipelinerun.sh <tag-name> [branch-spec]
```

| Argument | Description |
|----------|-------------|
| `tag-name` | The existing tag to rebuild (e.g., `v0.1.69`) |
| `branch-spec` | Branch with the updated pipeline (default: `main`). Format: `[fork:]branch-name` |

```bash
# Rebuild v0.1.69 using the pipeline from main
hack/tools/scripts/create-manual-tag-pipelinerun.sh v0.1.69

# Use a pipeline from a fork branch
hack/tools/scripts/create-manual-tag-pipelinerun.sh v0.1.69 celebdor:fix-tag-pipeline
```

Prerequisites: `oc`, `yq`

**`hack/tools/scripts/create-snapshot-from-pipelinerun.sh`** — creates a
Snapshot from a completed PipelineRun to trigger Enterprise Contract
validation.

```bash
# From a PipelineRun name
hack/tools/scripts/create-snapshot-from-pipelinerun.sh <pipelinerun-name>

# From explicit image details
hack/tools/scripts/create-snapshot-from-pipelinerun.sh \
  --image-url <url> --image-digest <digest> \
  [--commit <sha>] [--target-branch <branch>]
```

Prerequisites: `oc`, `jq`

## Validate CPO override images

When reviewing a PR that references CPO override images claiming to include
fixes from other PRs, use this to verify those claims before approving.

**Skill:** `/validate-pr-override-images` — inspects container image layers
via `skopeo` and compares commit SHAs to verify the override images actually
contain the claimed PRs.
