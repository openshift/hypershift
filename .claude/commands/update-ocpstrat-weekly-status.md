---
name: update-ocpstrat-weekly-status
description: Update OCPSTRAT weekly status for issues in OCPSTRAT (component can be specified or selected interactively)
plugin: project
---

# Weekly Status Update for OCPSTRAT Issues

This command updates the weekly status summary for OCPSTRAT issues. You can specify a component with `--component`, or if not provided, you'll be prompted to select from available components. It should be run every Wednesday morning.

## Parameters

The command accepts optional parameters:

- **Component**: Optional `--component <component-name>` parameter (e.g., `--component "Hosted Control Planes"`)
  - If not provided, the command will list all available components for OCPSTRAT and prompt for selection
- **User filters**: Space-separated list of user emails or names to include (e.g., `user1@redhat.com user2@redhat.com` or `Antoni Segura Dave Gordon`)
  - **Specific users**: Space-separated list of user emails or names to include
  - **Exclude users**: Prepend email or name with **!** to exclude (e.g., `!davegord@redhat.com` or `!Dave Gordon`)
  - **Mixed**: Can combine both (e.g., `antoni@redhat.com !Dave Gordon`)

**Name Resolution:**
- If a parameter doesn't contain `@` (not an email), treat it as a display name
- Use `mcp__atlassian-mcp__jira_get_user_profile` to look up the user
- Present the found user(s) to the user for confirmation before proceeding
- If multiple matches or no match found, ask the user to provide the email directly

## Instructions

### 1. Determine Target Component(s)

Parse the command arguments to check for `--component` parameter:

1. **If `--component` is provided:**
   - Extract the component name from the parameter value
   - Use it directly in the JQL query

2. **If `--component` is NOT provided:**
   - Use `mcp__atlassian-mcp__jira_search_fields` with keyword "component" to find the component field ID
   - Use `mcp__atlassian-mcp__jira_search` with JQL: `project = "OpenShift Container Platform (OCP) Strategy" AND status != Closed` and `fields=components`
   - Extract all unique component names from the search results
   - Present components in a numbered list:
     ```
     Available components for OCPSTRAT:
     1. Component Name 1
     2. Component Name 2
     3. Component Name 3
     ...
     ```
   - Ask the user: "Please enter the number(s) of the component(s) you want to update (space-separated, e.g., '1 3 5'):"
   - Parse the user's response and map the numbers back to component names
   - If multiple components are selected, process each component separately (run steps 2-5 for each component)

### 2. Resolve User Identifiers

For each parameter provided:

1. **Check if it's an email** (contains `@`):
   - If yes, use as-is

2. **If it's a name** (doesn't contain `@`):
   - Use `mcp__atlassian-mcp__jira_get_user_profile` with the name as the `user_identifier` parameter
   - The tool accepts display names, usernames, email addresses, or account IDs
   - When the user profile is returned, show the user:
     - Display name
     - Email address
     - Account ID
   - Ask for confirmation: "Found user: [Display Name] ([Email]). Is this correct?"
   - If confirmed, use the email address for the JQL query
   - If not confirmed or lookup fails, ask the user to provide the email address directly

3. **Handle exclusion prefix** (**!**):
   - Strip the **!** prefix before doing the lookup
   - Remember to apply the exclusion logic when building the JQL

### 3. Find Issues Requiring Updates

Build a JQL query based on the determined component(s) and resolved user filter parameters:

**Base query:**
```jql
project = "OpenShift Container Platform (OCP) Strategy" AND
status != Closed AND
status != "Release Pending" AND
labels = control-plane-work AND
component = "<COMPONENT-NAME>"
```

Replace `<COMPONENT-NAME>` with the component determined in step 1. If multiple components are selected, process each one separately.

**Add user filters:**
- If no parameters: Add `AND assignee != "davegord@redhat.com"` (default)
- If specific users provided (no **!** prefix): Add `AND assignee IN (email1, email2, ...)`
- If excluded users provided (**!** prefix): Add `AND assignee != "email1" AND assignee != "email2" ...`
- If mixed: Combine appropriately

**Final:**
```jql
ORDER BY rank ASC
```

Use `mcp__atlassian-mcp__jira_search` with this JQL.

### 4. For Each Issue

Process each issue with the following steps:

#### a. Gather Information Efficiently

**IMPORTANT**: Only fetch the fields you need to save context:
- Use `fields=summary,issuelinks,comment,customfield_12320841` when getting issue details
- Use `expand=changelog` to get field update history
- Set `comment_limit=20` to limit comment history

For each issue:
1. Check when Status Summary was last updated:
   - Look in the changelog for the most recent update to `customfield_12320841` (Status Summary field)
   - Calculate hours since last update
   - If updated within last 24 hours, flag for warning
2. Check recent non-automation comments (last 7 days)
3. Check child issues/Epics updated in last 7 days using JQL: `parent = <ISSUE-KEY> AND updated >= -7d`
4. Check linked GitHub PRs and GitLab MRs:
   - First get all child issues: `parent = <ISSUE-KEY>`
   - For each child issue, check the `issuelinks` field for links to GitHub or GitLab
   - Look for issue link types that point to external repositories (GitHub PRs, GitLab MRs)
   - For GitHub PRs found, use `gh pr view <PR-NUMBER> --repo openshift/hypershift --json state,updatedAt,mergedAt` to check activity
   - Check if PRs were updated/merged in the last 7 days
5. Check current status summary value: `fields=customfield_12320841`

#### b. Analyze and Draft Update

Based on the gathered information, draft a status update following this template:

```
* Color Status: {Red, Yellow, Green}
 * Status summary:
     ** Thing 1 that happened since last week
     ** Thing 2 that happened since last week
 * Risks:
     ** Risk 1 that might affect delivery
     ** Risk 2 that might affect delivery
```

**Color Status Guidelines:**
- **Green**: On track, good progress
- **Yellow**: Minor concerns, some blockers but manageable
- **Red**: Significant blockers, no progress, major risks

#### c. Present to User

Before updating, check if the Status Summary was updated in the last 24 hours:

**If updated within last 24 hours:**
- Show a warning: `⚠️  WARNING: This issue's Status Summary was last updated X hours ago (on YYYY-MM-DD at HH:MM).`
- Show the current status summary
- Ask: `This issue was recently updated. Do you want to skip it? (Recommended: yes)`
- If user says yes/skip, move to next issue
- If user says no/continue, proceed with showing proposed update

**For all issues (or if proceeding after warning):**
Show the user:
- Issue key and title
- Current status from the issue (if not already shown in warning)
- Your analysis of recent activity
- Proposed status update

Ask if they want to:
- Proceed with your proposed update
- Modify the text
- Skip the issue

#### d. Update the Issue

Use `mcp__atlassian-mcp__jira_update_issue` with:
```json
{
  "issue_key": "OCPSTRAT-XXXX",
  "fields": {
    "customfield_12320841": "<formatted status text>"
  }
}
```

**IMPORTANT**: The Status Summary field (customfield_12320841) requires exact formatting with bullet points as shown in the template above.

### 5. Important Notes

- **Recent Updates Warning**: Always check the changelog for Status Summary updates in the last 24 hours and warn the user with a recommendation to skip
- **Efficiency**: Don't fetch all fields (`*all`) - only get what you need. Always use `expand=changelog` to get update history
- **User Input**: Accept user corrections and exact text for status updates
- **Skip Issues**: Allow user to skip issues when requested, especially those recently updated
- **Context Labels**: Note if issues have labels like `aro`, `rosa-hcp`, or `no_core_payload` that indicate non-core-payload work
- **PR/MR Checking**: Check child tickets for GitHub PR and GitLab MR links, not the parent OCPSTRAT ticket directly, as child tickets (Stories/Tasks) contain the actual code references

### 6. Summary

After processing all issues, provide a summary of:
- Total issues processed
- Issues updated (with status colors)
- Issues skipped

## Example Usage

**Default (will prompt for component selection, exclude Dave Gordon):**
```
/update-ocpstrat-weekly-status
```

**With specific component (Hosted Control Planes):**
```
/update-ocpstrat-weekly-status --component "Hosted Control Planes"
```

**Specific component and users (by email):**
```
/update-ocpstrat-weekly-status --component "Hosted Control Planes" antoni@redhat.com jparrill@redhat.com
```

**Prompt for component, specific users only (by name - will prompt for confirmation):**
```
/update-ocpstrat-weekly-status Antoni Segura Juan Manuel Parrilla
```

**With component, exclude specific users (by email):**
```
/update-ocpstrat-weekly-status --component "Hosted Control Planes" !davegord@redhat.com !otheruser@redhat.com
```

**Prompt for component, exclude specific users (by name - will prompt for confirmation):**
```
/update-ocpstrat-weekly-status !Dave Gordon
```

**With component, mixed (include some, exclude others):**
```
/update-ocpstrat-weekly-status --component "Hosted Control Planes" antoni@redhat.com !Dave Gordon
```

The command will:
1. Determine target component(s) - either from --component parameter or by prompting user with numbered list
2. Parse user filter parameters
3. Resolve any names to email addresses using Jira user lookup (with confirmation)
4. Build appropriate JQL query with selected component(s) and resolved user filters
5. Search for all matching OCPSTRAT issues
6. Process each one systematically
7. Update Jira with weekly status summaries
8. Provide a final summary

## Custom Field Reference

- **Status Summary**: `customfield_12320841`
- This field stores the weekly status update in the specific bullet-point format shown above
