---
description: Automatically update outdated Konflux Tekton tasks based on enterprise contract verification logs.
---

Automatically update outdated Konflux Tekton tasks based on enterprise contract verification logs.

[Extended thinking: This command parses enterprise contract logs to identify outdated Tekton tasks, uses the Python script to efficiently query the quay.io registry API for version mappings, checks migration notes for breaking changes, and updates the common pipeline YAML file in the .tekton/ directory with the latest versions.]

**Konflux Tekton Tasks Update**

## Usage Examples:

1. **Update tasks from enterprise contract log**:
   `/update-konflux-tasks ../../hypershift-operator-enterprise-contract-lxgvw-verify.log`

## Implementation Details:

- Parses Konflux enterprise contract verification logs for outdated task warnings
- Uses the Python script `hack/tools/scripts/konflux_task_version_lookup.py` to efficiently query the quay.io registry API
- Handles Docker Registry API pagination correctly to get all semver tags
- Prioritizes EC-recommended versions from `tasks.unsupported` violations over digest matching
- Checks migration documentation for version bumps
- Requires Python 3.8+ and aiohttp

## Process Flow:

1. **Parse Enterprise Contract Log Using Python Script**:
   - Run the Python script: `python3 hack/tools/scripts/konflux_task_version_lookup.py {{args.0}} --output json`
   - The script parses the EC log using the `STEP-REPORT-JSON` delimiter (not regex)
   - Extracts both `trusted_task.current` warnings (digest updates) and `tasks.unsupported` violations (version bumps required)
   - For `tasks.unsupported` violations, the EC explicitly recommends a target version - this takes priority

2. **Map Latest Digests to Version Tags**:
   - The Python script uses async HTTP requests to the quay.io registry API
   - Properly handles pagination via `Link` headers to get all semver tags
   - Version resolution priority:
     1. EC-recommended version (from `tasks.unsupported` violation) - highest priority
     2. Digest match - if latest_digest matches a semver tag
     3. Highest available semver version - fallback
   - When using EC-recommended or highest-available, looks up the correct digest for that version
   - Returns JSON output with task mappings

3. **Check for Migration Notes**:
   - For any tasks with version bumps (not just digest updates), check for migration notes
   - If a task matches quay.io/redhat-appstudio-tekton-catalog/ rather than quay.io/konflux-ci/tekton-catalog, check if it is available in quay.io/konflux-ci/tekton-catalog and change to use the latter
   - Use URL pattern: `https://github.com/konflux-ci/build-definitions/blob/main/task/{task-name}/{version}/MIGRATION.md`
   - If the migration notes reference a migration script, check their availability with the pattern: `https://github.com/konflux-ci/build-definitions/blob/main/task/{task-name}/{version}/migrations/{version}.sh`. If it is available:
     - Run the migration script on the identified pipeline files
   - Extract any breaking changes, new parameters, or manual steps required
   - Ask the user for input if any manual steps are required
   - Report if "No action required" or list specific migration steps

4. **Update Pipeline Files**:
   - Update the common Tekton pipeline YAML file: `.tekton/pipelines/common-operator-build.yaml`
   - This is the shared pipeline referenced by all component-specific PipelineRun files
   - Replace old `quay.io/konflux-ci/tekton-catalog/task-{name}:{old-version}@{old-digest}` with new versions
   - Use Edit tool for each task update

5. **Provide Comprehensive Summary**:
   - List all outdated tasks found and their update status
   - Show current vs. latest version mappings
   - Highlight any version bumps vs. digest-only updates
   - Indicate the version resolution source (ec_recommended, digest_match, highest_available)
   - Report any migration notes or manual steps required
   - List all files updated
   - Provide before/after examples for key changes

## Expected Output Format:
```markdown
## Konflux Tekton Tasks Update Complete

### Tasks Updated:
- apply-tags: 0.2@old-digest -> 0.3@new-digest (VERSION BUMP via ec_recommended)
- build-image-index: 0.1@old-digest -> 0.2@new-digest (VERSION BUMP via ec_recommended)
- init: 0.2@old-digest -> 0.2@new-digest (digest update)
[... etc for all tasks]

### Files Updated:
- .tekton/pipelines/common-operator-build.yaml (8 tasks updated)

### Migration Notes:
- build-image-index v0.1->v0.2: No action required

### Summary:
- Total tasks updated: X
- Version bumps: X (with migration notes checked)
- Digest updates: X
- Files updated: 1
- Manual steps required: None / [list steps]
```

## Error Handling:
- If log file doesn't exist, provide clear error message
- If Python script fails, check that aiohttp is installed: `pip install aiohttp`
- If no outdated tasks found, report success with no changes needed
- If migration notes URL returns 404, note that no migration documentation exists
- If migration notes include changes to parameters, output or manual steps, prompt the user about them
- If EC-recommended version not found in registry, fall back to highest available and warn

## Safety Features:
- Preserves version tags (e.g., keeps `0.2` in `task:0.2@sha256:...`)
- Prioritizes EC-recommended versions to ensure compliance
- Checks migration notes for breaking changes before version bumps
- Provides detailed summary of all changes made
- Use TodoWrite to track progress through updates

## Requirements:
- Python 3.8+ with aiohttp (`pip install aiohttp`)
- Internet connectivity (to query quay.io registry and check migration notes)

## Arguments:
- {{args.0}}: Path to the enterprise contract verification log file that contains outdated task warnings (required)

The command will provide progress updates and automatically update the common pipeline file with the latest task versions.
