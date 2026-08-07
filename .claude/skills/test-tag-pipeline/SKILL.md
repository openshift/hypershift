---
name: test-tag-pipeline
description: >
  Create a manual Konflux PipelineRun to test tag pipeline changes before merging.
  Use when you have modified a Tekton tag pipeline definition and want to validate it
  by rebuilding an existing tag without merging first. Requires oc CLI login to the
  Konflux cluster and the tag to already exist.
---

# Test Tag Pipeline

Create a manual PipelineRun to test tag pipeline changes before merging, using an
existing tag's commit with an updated pipeline definition from a branch.

## Usage

```
/skill:test-tag-pipeline <tag-name> [branch-spec]
```

**Arguments:**
- `tag-name` (required): The existing tag to rebuild (e.g., `v0.1.69`)
- `branch-spec` (optional): Branch containing the updated pipeline (defaults to `main`)
  - Format: `[fork:]branch-name`
  - If no fork specified, defaults to `openshift`

**Examples:**
```
/skill:test-tag-pipeline v0.1.69
/skill:test-tag-pipeline v0.1.69 build-gomaxprocs-image
/skill:test-tag-pipeline v0.1.69 celebdor:OCPBUGS-63194-part2
```

## Steps

### 1. Verify Authentication to Konflux

```bash
oc whoami
```

If authentication fails, tell the user to log in:
```bash
oc login --web https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443
```

### 2. Create the PipelineRun

**Important:** Use only the arguments provided by the user. If no branch argument is
given, default to `main`. Do NOT substitute the current git branch.

```bash
bash hack/tools/scripts/create-manual-tag-pipelinerun.sh <tag-name> <branch-spec:-main>
```

After creation, extract the PipelineRun name and construct the web UI URL:
- Base: `https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com`
- Pattern: `/ns/crt-redhat-acm-tenant/applications/hypershift-operator/pipelineruns/{name}`

Display the full URL for the user to monitor progress.

CLI monitoring:
```bash
oc get pipelinerun <name> -w
```

### 3. Create Snapshot for Enterprise Contract Validation

After the PipelineRun completes successfully:

```bash
bash hack/tools/scripts/create-snapshot-from-pipelinerun.sh <pipelinerun-name>
```

Get the EC PipelineRuns:
```bash
oc get pipelinerun -l appstudio.openshift.io/snapshot=<snapshot-name> -o name
```

Display web UI URLs for each EC PipelineRun (same base URL pattern).

CLI monitoring:
```bash
oc get snapshot <snapshot-name> -w
oc get pipelinerun -l appstudio.openshift.io/snapshot=<snapshot-name>
```

## Notes

- Useful for testing pipeline fixes before merging PRs
- The PipelineRun uses the updated pipeline definition but builds the original tag's commit
- Two-step process: PipelineRun completes (20+ minutes), then Snapshot triggers EC validation
- Requires `yq` and `oc` CLI tools
- See `hack/tools/scripts/create-manual-tag-pipelinerun.sh` and `hack/tools/scripts/create-snapshot-from-pipelinerun.sh` for implementation details
