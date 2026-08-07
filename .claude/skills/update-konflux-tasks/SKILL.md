---
name: update-konflux-tasks
description: >
  Automatically update outdated Konflux Tekton tasks in pipeline YAML files. Use when
  enterprise contract verification reports outdated task bundles, or when you want to
  proactively detect and apply Tekton task updates. Maps digests to version tags, checks
  migration notes for breaking changes, and updates all pipeline files in .tekton/.
---

# Update Konflux Tekton Tasks

Detect outdated Tekton tasks and update all pipeline YAML files with the latest versions,
checking migration notes for breaking changes along the way.

## Usage

```
/skill:update-konflux-tasks [log-file-path]
```

**Arguments:**
- `log-file-path` (optional): Path to an enterprise contract verification log file. When omitted, uses the detection script to find updates automatically.

**Examples:**
```
/skill:update-konflux-tasks ../../hypershift-operator-enterprise-contract-lxgvw-verify.log
/skill:update-konflux-tasks
```

## Process Flow

### 1. Detect Outdated Tasks

**With log file:**
- Read the provided log file
- Extract all outdated Tekton task warnings mentioning "newer version exists"
- Parse task names, current digests, and latest digests

**Without log file:**
- Run the detection script:
  ```bash
  hack/tools/scripts/update_trusted_task_bundles.py $(find .tekton -name '*.yaml') \
    --dry-run --json --upgrade-versions
  ```
- Parse JSON output for tasks needing updates (includes `task_name`, `current_version`, `current_digest`, `latest_version`, `latest_digest`, `is_version_bump`)

### 2. Map Latest Digests to Version Tags

For each outdated task:
```bash
hack/tools/scripts/find_task_version_by_digest.sh <task-name> <digest>
```

The helper script uses `skopeo list-tags` and `skopeo inspect` to find which semantic version tag (e.g., 0.2, 0.3) matches the digest. It filters out commit-hash style tags.

Create a mapping: task-name → current-version@digest → latest-version@digest

### 3. Check for Migration Notes

For tasks with version bumps (not just digest updates):
- Check if task uses `quay.io/redhat-appstudio-tekton-catalog/` — if so, check if available in `quay.io/konflux-ci/tekton-catalog` and switch to the latter
- Check migration notes at: `https://github.com/konflux-ci/build-definitions/blob/main/task/{task-name}/{version}/MIGRATION.md`
- Check for migration scripts at: `https://github.com/konflux-ci/build-definitions/blob/main/task/{task-name}/{version}/migrations/{version}.sh`
  - If available, run the migration script on identified pipeline files
- Extract breaking changes, new parameters, or manual steps
- Ask the user about any manual steps required
- Report "No action required" or list specific migration steps

### 4. Update Pipeline Files

- Discover all Tekton pipeline YAML files: `.tekton/**/*.yaml`
- Replace old `quay.io/konflux-ci/tekton-catalog/task-{name}:{old-version}@{old-digest}` with new versions
- Edit multiple locations efficiently when updating multiple tasks per file

### 5. Provide Summary

```markdown
## 🔄 Konflux Tekton Tasks Update Complete

### Tasks Updated:
- ✅ apply-tags: 0.2@old-digest → 0.2@new-digest (digest update)
- ✅ buildah-remote-oci-ta: 0.4@old-digest → 0.5@new-digest (VERSION BUMP - migration notes checked)
- ✅ init: 0.2@old-digest → 0.2@new-digest (digest update)

### Files Updated:
- ✅ .tekton/hypershift-operator-main-push.yaml (8 tasks updated)
- ✅ .tekton/hypershift-operator-main-pull-request.yaml (8 tasks updated)

### Migration Notes:
- buildah-remote-oci-ta v0.4→v0.5: ✅ No action required (bug fix for SBOM generation)

### Summary:
- Total tasks updated: X
- Version bumps: X (with migration notes checked)
- Digest updates: X
- Files updated: X
- Manual steps required: None / [list steps]
```

## Error Handling

| Scenario | Action |
|----------|--------|
| Log file doesn't exist | Clear error message |
| Detection script fails | Show error details |
| `skopeo` not installed | Provide installation instructions |
| `jq` not installed | Provide installation instructions |
| `yq` not installed | Provide installation instructions |
| `pyyaml` not installed | `pip install pyyaml` |
| No outdated tasks found | Report success, no changes needed |
| Migration notes 404 | Note no migration documentation exists |
| Migration notes have manual steps | Ask the user about them |

## Safety Features

- Preserves version tags (e.g., keeps `0.2` in `task:0.2@sha256:...`)
- Checks migration notes for breaking changes before major version bumps
- Provides detailed summary of all changes

## Requirements

- `skopeo` (container image inspection)
- `jq` (JSON parsing)
- `yq` (YAML parsing and multi-platform build checks)
- `pyyaml` Python package (when running without a log file)
- Internet connectivity (migration notes and image inspection)
