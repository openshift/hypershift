---
description: Automatically update outdated Konflux Tekton tasks based on enterprise contract verification logs.
---

Automatically update outdated Konflux Tekton tasks based on enterprise contract verification logs or by detecting updates directly.

[Extended thinking: This command can either parse enterprise contract logs or use the update_trusted_task_bundles.py script to identify outdated Tekton tasks, uses skopeo to map digests to version tags, checks migration notes for breaking changes, and updates all pipeline YAML files in the .tekton/ directory with the latest versions.]

**Konflux Tekton Tasks Update**

## Usage Examples:

1. **Update tasks from enterprise contract log**:
   `/update-konflux-tasks ../../hypershift-operator-enterprise-contract-lxgvw-verify.log`

2. **Detect and update tasks automatically (no log file)**:
   `/update-konflux-tasks`

## Implementation Details:

- When a log file is provided: Parses Konflux enterprise contract verification logs for outdated task warnings
- When no log file is provided: Uses `hack/tools/scripts/update_trusted_task_bundles.py --dry-run --json` to detect updates
- Uses `skopeo inspect` to map SHA256 digests to proper version tags
- Checks migration documentation for version bumps
- Requires `skopeo` and `jq` to be installed

## Process Flow:

1. **Detect Outdated Tasks**:
   - If a log file is provided ({{args.0}}):
     - Read the provided log file
     - Extract all outdated Tekton task warnings that mention "newer version exists"
     - Parse out task names, current digests, and latest digests
   - If no log file is provided:
     - Run `hack/tools/scripts/update_trusted_task_bundles.py .tekton/*.yaml --dry-run --json`
     - Parse the JSON output to identify tasks needing updates
     - The JSON output contains updates and available_upgrades per file with task_name, current_version, current_digest, latest_version, latest_digest, and is_version_bump fields

2. **Map Latest Digests to Version Tags**:
   - For each outdated task, use the helper script `hack/tools/scripts/find_task_version_by_digest.sh <task-name> <digest>` to determine the proper version tag for the latest digest
   - The helper script uses `skopeo list-tags` and `skopeo inspect` to find which semantic version tag (e.g., 0.2, 0.3) matches the given digest
   - It filters out commit-hash style tags (e.g., "0.2-f788d9b...") and only returns clean semantic versions
   - Create a mapping of: task-name → current-version@digest → latest-version@digest

3. **Check for Migration Notes**:
   - For any tasks with version bumps (not just digest updates), check for migration notes
   - If a task matches quay.io/redhat-appstudio-tekton-catalog/ rather than quay.io/konflux-ci/tekton-catalog, we should check if it is available in quay.io/konflux-ci/tekton-catalog and change to use the latter.
   - Use URL pattern: `https://github.com/konflux-ci/build-definitions/blob/main/task/{task-name}/{version}/MIGRATION.md`
   - If the migration notes reference a migration script, check their availability with the pattern: `https://github.com/konflux-ci/build-definitions/blob/main/task/{task-name}/{version}/migrations/{version}.sh`. If it is available:
     - Run the migration script on the identified pipeline files
   - Extract any breaking changes, new parameters, or manual steps required
   - Ask the user for input in any manual steps are required
   - Report if "No action required" or list specific migration steps

4. **Update Pipeline Files**:
   - Update all Tekton pipeline YAML files in `.tekton/` directory:
     - `hypershift-operator-main-push.yaml`
     - `hypershift-operator-main-pull-request.yaml`
     - `hypershift-operator-main-tag.yaml`
     - `control-plane-operator-main-push.yaml`
     - `control-plane-operator-main-pull-request.yaml`
     - `hypershift-shared-ingress-main-push.yaml`
     - `hypershift-shared-ingress-main-pull-request.yaml`
   - Replace old `quay.io/konflux-ci/tekton-catalog/task-{name}:{old-version}@{old-digest}` with new versions
   - Use MultiEdit for efficiency when updating multiple tasks per file

5. **Provide Comprehensive Summary**:
   - List all outdated tasks found and their update status
   - Show current vs. latest version mappings
   - Highlight any version bumps vs. digest-only updates
   - Report any migration notes or manual steps required
   - List all files updated
   - Provide before/after examples for key changes

## Expected Output Format:
```markdown
## 🔄 Konflux Tekton Tasks Update Complete

### Tasks Updated:
- ✅ apply-tags: 0.2@old-digest → 0.2@new-digest (digest update)
- ✅ buildah-remote-oci-ta: 0.4@old-digest → 0.5@new-digest (VERSION BUMP - migration notes checked)
- ✅ init: 0.2@old-digest → 0.2@new-digest (digest update)
[... etc for all tasks]

### Files Updated:
- ✅ .tekton/hypershift-operator-main-push.yaml (8 tasks updated)
- ✅ .tekton/hypershift-operator-main-pull-request.yaml (8 tasks updated)
[... etc for all files]

### Migration Notes:
- buildah-remote-oci-ta v0.4→v0.5: ✅ No action required (bug fix for SBOM generation)

### Summary:
- Total tasks updated: X
- Version bumps: X (with migration notes checked)
- Digest updates: X
- Files updated: X
- Manual steps required: None / [list steps]
```

## Error Handling:
- If log file is provided but doesn't exist, provide clear error message
- If no log file is provided and update_trusted_task_bundles.py fails, provide error details
- If skopeo is not installed, provide installation instructions
- If jq is not installed, provide installation instructions
- If yq is not installed, provide installation instructions
- If PyYAML is not installed (required for update_trusted_task_bundles.py), provide installation instructions: `pip install pyyaml`
- If no outdated tasks found, report success with no changes needed
- If migration notes URL returns 404, note that no migration documentation exists
- If migration notes include changes that to parameters, output or manual steps, prompt the user about them 

## Safety Features:
- ✅ Preserves version tags (e.g., keeps `0.2` in `task:0.2@sha256:...`)
- ✅ Checks migration notes for breaking changes before major version bumps
- ✅ Provides detailed summary of all changes made
- ✅ Use TodoWrite to track progress through complex multi-file updates

## Requirements:
- `skopeo` must be installed (for container image inspection)
- `jq` must be installed (for JSON parsing)
- `yq` must be installed (for YAML parsing and checking multi-platform builds)
- `pyyaml` Python package must be installed when running without a log file (for update_trusted_task_bundles.py)
- Internet connectivity (to check migration notes and inspect container images)

## Arguments:
- {{args.0}}: Path to the enterprise contract verification log file that contains outdated task warnings (optional). When not provided, the skill uses `hack/tools/scripts/update_trusted_task_bundles.py --dry-run --json` to automatically detect outdated tasks.

The command will provide progress updates and automatically update all relevant Tekton pipeline files with the latest task versions.
