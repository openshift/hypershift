---
description: "Process Renovate dependency PR(s) to meet repository contribution standards"
argument-hint: "Pass PR number or 'open' as $1, optionally Jira project as $2 (default: CNTRLPLANE), and component as $3 (default: HyperShift)"
---

# General Instructions

You are processing automated Renovate/Konflux dependency update PR(s) to meet repository contribution standards.

## Parameters

- **$1** (required): PR number OR "open" to process all open PRs from Konflux
- **$2** (optional): Jira project key (default: "CNTRLPLANE")
- **$3** (optional): Jira component name (default: "HyperShift")

## Validation

### If $1 is a PR number:
1. Verify it's a valid PR number
2. Check that the PR is from `red-hat-konflux[bot]`
3. Verify the PR title matches pattern: `chore(deps): update * digest to * (main)`
4. Ensure this is NOT a "Pipelines as Code configuration" PR (check PR body doesn't contain "Pipelines as Code configuration proposal")
5. Process this single PR

### If $1 is "open":
1. Get all open PRs from `red-hat-konflux[bot]`: `gh pr list --author="red-hat-konflux[bot]" --state=open`
2. Filter out "Pipelines as Code configuration" PRs by checking PR bodies
3. Process each remaining dependency PR one by one
4. Provide progress updates: "Processing PR #X of Y"

## Workflow Steps

Execute the following steps in order:

### 1. Determine Target OpenShift Version

- Fetch the latest state from origin: `git fetch origin`
- Get the commit hash for `origin/main`
- Find all release branches that match main's commit: `origin/release-*`
- Select the **lowest** version number from matching branches (e.g., if both 4.21 and 4.22 match, use 4.21)
- Store this as the target version (e.g., "openshift-4.21")

### 2. Analyze the Dependency

From the PR diff (go.mod changes):

**Determine if Direct or Indirect:**
- Check if the dependency line has `// indirect` comment in go.mod
- If indirect: identify the direct parent dependency that pulls it in
  - Use `go mod why <package>` or check vendor directory structure
  - Document the dependency chain

**Analyze Usage:**
- For **direct dependencies**: Search codebase for import statements and usage
  - Identify which files import this dependency
  - Determine the purpose (e.g., "OpenStack image management", "AWS integration")
  - Note if it's runtime code vs tooling (check if in hack/tools/)

- For **indirect dependencies**: Focus on the direct parent
  - Document what the parent dependency is used for
  - Note if it's development tooling vs runtime

**Check Version Changes:**
- Extract old and new pseudo-versions from go.mod
- If possible, fetch upstream commit messages between versions using GitHub API
- Categorize as: patch/minor/major or "digest update with enhancements/fixes"

**Determine Testing Strategy:**
- For hack/tools dependencies: Find Makefile targets that use the tool
- For runtime dependencies: Suggest relevant component testing
- Provide specific commands to validate the update

### 3. Check for Existing Jira Ticket

- Get all PR comments using: `gh pr view <PR_NUMBER> --json comments`
- Search comments for references to the Jira project (use $2 if provided, otherwise "CNTRLPLANE")
- If found, skip Jira creation and use existing ticket
- If not found, proceed to create new ticket

### 4. Create Jira Ticket

Create a comprehensive Jira ticket:

**Required Fields:**
- `project_key`: Use $2 if provided, otherwise "CNTRLPLANE"
- `summary`: "{Package name} ({Brief purpose description})"
  - Example: "Update mvdan.cc/unparam (Go linter tool dependency)"
- `issue_type`: "Task"
- `components`: Use $3 if provided, otherwise "HyperShift"
- `additional_fields`:
  - `labels`: ["dependencies", "renovate", "ai-generated" and optional context labels like "tooling", "openstack", "aws", etc.]

**Description Template:**
```markdown
## Overview
Update {package} from digest {old} to {new}.

## Dependency Information
- **Package**: {full package path}
- **Type**: {Direct/Indirect} dependency {if indirect: (pulled in by {parent})}
- **Direct parent**: {if indirect, name the direct dependency}
- **Old version**: {old pseudo-version}
- **New version**: {new pseudo-version}
- **Location**: {go.mod path, e.g., hack/tools/go.mod or go.mod}

## Usage in {Repository Name}
{Description of how this dependency is used}
- **Primary usage**: {Main purpose}
- **{Additional context}**: {Details}
- **Files affected**: {List key files if direct dependency}
- **Impact**: {Runtime vs development tooling}

Note: Get {Repository Name} by:
1. Running: `git remote -v`
2. Finding the non-fork remote (typically "origin" pointing to github.com/openshift/* or similar org)
3. Extract repository name from the URL (e.g., "hypershift" from "git@github.com:openshift/hypershift")

## Changes in Update
{Summary of upstream changes, features, fixes based on commit analysis}

## How to Test
{Specific, actionable testing instructions}

1. **{Step 1}:**
   ```bash
   {command}
   ```

2. **{Step 2}:**
   ```bash
   {command}
   ```

{Continue with all testing steps}

**Expected behavior:** {What should happen when tests pass}
```

**After Creating Ticket:**
- Update with Target Version using:
  ```
  fields: {"customfield_12319940": [{"name": "openshift-X.Y"}]}
  ```
  Where X.Y is the version from step 1

### 5. Update PR Title

Post a comment on the PR with (use $2 for project key if provided, otherwise "CNTRLPLANE"):
```
/retitle [{PROJECT}-XXXX](https://issues.redhat.com/browse/{PROJECT}-XXXX): {Package name} ({Brief description})

---

This PR has been processed to meet repository contribution standards:
- ✅ Analyzed dependency type {and usage in codebase OR and dependency chain}
- ✅ {Identified direct parent and usage in codebase OR Code usage and impact assessment}
- ✅ Created Jira ticket with comprehensive {details and testing instructions OR testing procedures}
- ✅ Set Target Version to openshift-X.Y
- ✅ Updated PR title with Jira reference

For more information about this dependency update, including:
- {Detailed dependency analysis (direct vs indirect) OR Dependency chain analysis}
- {Code usage and impact assessment OR Development tooling impact assessment}
- {Upstream changes included in this update OR Testing procedures}
- {Step-by-step testing instructions OR Make targets for validation}

Please see the linked Jira ticket: [{PROJECT}-XXXX](https://issues.redhat.com/browse/{PROJECT}-XXXX)

---
🤖 Assisted by [Claude Code](https://claude.com/claude-code)
```

Note: Replace {PROJECT} with the actual Jira project key ($2 or "CNTRLPLANE").
Adjust the checklist items based on whether it's a direct or indirect dependency.

## Error Handling

- If PR is not from Konflux bot, explain and exit
- If PR is a pipeline configuration update, explain this command only handles dependency updates
- If Jira creation fails, provide the ticket content for manual creation
- If version field update fails, note it may need manual setting

## Output Format

### For single PR:
```
✅ Processed PR #{number}
- Dependency: {package name}
- Type: {Direct/Indirect}
- Jira: {PROJECT}-XXXX
- Target Version: openshift-X.Y
- PR title updated with Jira reference
```

### For "open" (multiple PRs):
```
Processing {N} dependency PRs...

[1/N] Processing PR #{number}...
✅ Completed PR #{number}
- Dependency: {package name}
- Jira: {PROJECT}-XXXX

[2/N] Processing PR #{number}...
✅ Completed PR #{number}
- Dependency: {package name}
- Jira: {PROJECT}-XXXX

...

Summary:
✅ Processed {N} PRs successfully
- Jira project: {PROJECT}
- Component: {Component Name}
- Target Version: openshift-X.Y
```
