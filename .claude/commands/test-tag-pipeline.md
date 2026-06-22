Create a manual PipelineRun to test tag pipeline changes before merging.

**Usage**: `/test-tag-pipeline <tag-name> [branch-spec]`

**Arguments**:
- `tag-name` (required): The existing tag to rebuild (e.g., `v0.1.69`)
- `branch-spec` (optional): The branch containing the updated pipeline (defaults to `main`)
  - Format: `[fork:]branch-name`
  - If no fork specified, defaults to `openshift`

**What this does**:
1. Verifies you are logged into the Konflux instance
2. Gets the commit SHA that the tag points to
3. Fetches the tag pipeline definition from the specified branch/fork
4. Replaces template variables with actual values for the tag
5. Creates a manual PipelineRun that uses the updated pipeline to build the tag's commit
6. Outputs the PipelineRun name for monitoring

**Examples**:
```bash
# Test main branch pipeline with v0.1.69 tag
/test-tag-pipeline v0.1.69

# Test PR branch pipeline with v0.1.69 tag
/test-tag-pipeline v0.1.69 build-gomaxprocs-image

# Test fork branch pipeline with v0.1.69 tag
/test-tag-pipeline v0.1.69 celebdor:OCPBUGS-63194-part2
```

**Implementation**:

IMPORTANT: Execute the commands exactly as shown with only the arguments provided by the user. If no branch argument is specified, the template `{{args.1:-main}}` will correctly default to `main`. Do NOT substitute the current git branch or any other inferred values.

Step 1: Verify authentication to Konflux
```bash
oc whoami
```

If the authentication check fails, inform the user they need to log in first:
```bash
oc login --web https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443
```

Step 2: Create the PipelineRun
```bash
bash hack/tools/scripts/create-manual-tag-pipelinerun.sh {{args.0}} {{args.1:-main}}
```

After the PipelineRun is created, extract the name from the output and construct the web UI URL:
- Base URL: https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com
- Pattern: /ns/crt-redhat-acm-tenant/applications/hypershift-operator/pipelineruns/{pipelinerun-name}

Example: https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com/ns/crt-redhat-acm-tenant/applications/hypershift-operator/pipelineruns/hypershift-operator-main-manual-v0.1.69-xxxxx

Display the full URL to the user so they can monitor progress in the web UI.

You can also monitor it from the CLI with:
```bash
# Watch the PipelineRun status
oc get pipelinerun <name> -w
```

Step 3: After the PipelineRun completes successfully, create a Snapshot to trigger Enterprise Contract validation:
```bash
bash hack/tools/scripts/create-snapshot-from-pipelinerun.sh <pipelinerun-name>
```

After the Snapshot is created, extract the snapshot name from the output and get the EC PipelineRuns:
```bash
oc get pipelinerun -l appstudio.openshift.io/snapshot=<snapshot-name> -o name
```

For each EC PipelineRun, construct and display the web UI URL:
- Base URL: https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com
- Pattern: /ns/crt-redhat-acm-tenant/applications/hypershift-operator/pipelineruns/{ec-pipelinerun-name}

Example: https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com/ns/crt-redhat-acm-tenant/applications/hypershift-operator/pipelineruns/hypershift-operator-enterprise-contract-xxxxx

Display the full URLs to the user so they can monitor EC validation in the web UI.

You can also monitor from the CLI with:
```bash
# Watch the snapshot status
oc get snapshot <snapshot-name> -w

# Check EC test results
oc get pipelinerun -l appstudio.openshift.io/snapshot=<snapshot-name>
```

**Notes**:
- This is useful for testing pipeline fixes before merging PRs
- The PipelineRun uses the updated pipeline definition but builds the original tag's commit
- The two-step process allows the PipelineRun to complete (20+ minutes) before creating the Snapshot
- Requires `yq` and `oc` CLI tools to be installed
- See `hack/tools/scripts/create-manual-tag-pipelinerun.sh` and `hack/tools/scripts/create-snapshot-from-pipelinerun.sh` for implementation details
