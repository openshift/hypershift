---
description: Generate manual test steps for a JIRA issue
---

Generate comprehensive manual test steps for a JIRA issue by analyzing related pull requests.

[Extended thinking: This command takes a JIRA issue key and optionally a list of PR URLs. It fetches the JIRA issue details, retrieves all related PRs (or uses the provided PR list), analyzes the changes, and generates a comprehensive manual testing guide.]

**JIRA Issue Test Guide Generator**

## Usage Examples:

1. **Generate test steps for JIRA with auto-discovered PRs**:
   `/jira-test CNTRLPLANE-205`

2. **Generate test steps for JIRA with specific PRs only**:
   `/jira-test CNTRLPLANE-205 https://github.com/openshift/hypershift/pull/6888`

3. **Generate test steps for multiple specific PRs**:
   `/jira-test CNTRLPLANE-205 https://github.com/openshift/hypershift/pull/6888 https://github.com/openshift/hypershift/pull/6889`

## Implementation Details:

- The command uses curl to fetch JIRA data via REST API: https://issues.redhat.com/rest/api/2/issue/{$1}
- Uses WebFetch to extract PR links from JIRA issue if no PRs provided
- Uses `gh pr view` to fetch PR details for each PR
- Analyzes changes across all PRs to understand implementation
- Generates comprehensive manual test scenarios

## Process Flow:

1. **JIRA Analysis**: Fetch and parse JIRA issue details:
   - Use curl to fetch JIRA issue data: `curl -s "https://issues.redhat.com/rest/api/2/issue/{$1}"`
   - Parse JSON response to extract:
     - Issue summary and description
     - Context and acceptance criteria
     - Steps to reproduce (for bugs)
     - Expected vs actual behavior
   - Extract issue type (Story, Bug, Task, etc.)

2. **PR Discovery**: Get list of PRs to analyze:
   - **If no PRs provided in arguments** ($2, $3, etc. are empty):
     - Use WebFetch on https://issues.redhat.com/browse/{$1}
     - Extract all GitHub PR links from:
       - "Issue Links" section
       - "Development" section
       - PR links in comments
     - Filter to only openshift/hypershift PRs
   - **If PRs provided in arguments**:
     - Use only the PRs provided in $2, $3, $4, etc.
     - Ignore any other PRs linked to the JIRA

3. **PR Analysis**: For each PR, fetch and analyze:
   - Use `gh pr view {PR_NUMBER} --repo openshift/hypershift --json title,body,commits,files,labels`
   - Extract:
     - PR title and description
     - Changed files and their diffs
     - Commit messages
     - PR status (merged, open, closed)
   - Read changed files to understand implementation details
   - Use Grep and Glob tools to:
     - Find related test files
     - Locate configuration or documentation
     - Identify dependencies

4. **Change Analysis**: Understand what was changed across all PRs:
   - Identify the overall objective (bug fix, feature, refactor)
   - Determine affected components (API, CLI, operator, control-plane, etc.)
   - Find platform-specific changes (AWS, Azure, KubeVirt, etc.)
   - Map which PR addresses which aspect of the JIRA
   - Identify any dependencies between PRs

5. **Test Scenario Generation**: Create comprehensive test plan:
   - Map JIRA acceptance criteria to test scenarios
   - For bugs: Use reproduction steps as test cases
   - Generate test scenarios covering:
     - Happy path scenarios (based on acceptance criteria)
     - Edge cases and error handling
     - Platform-specific variations if applicable
     - Regression scenarios
   - For multiple PRs:
     - Create integrated test scenarios
     - Verify PRs work correctly together
     - Test each PR's contribution to the overall solution

6. **Test Guide Creation**: Generate detailed manual testing document:
   - **Filename**: Always use JIRA key format: `test-{JIRA_KEY}.md`
     - Convert JIRA key to lowercase
     - Examples: `test-cntrlplane-205.md`, `test-ocpbugs-12345.md`
   - **Structure**:
     - **JIRA Summary**: Include JIRA key, title, description, acceptance criteria
     - **PR Summary**: List all PRs with titles and how they relate to the JIRA
     - **Prerequisites**:
       - Required infrastructure and tools
       - Environment setup requirements
       - Access requirements
     - **Test Scenarios**:
       - Map each test to JIRA acceptance criteria
       - Numbered test cases with clear steps
       - Expected results with verification commands
       - Platform-specific test variations
     - **Regression Testing**:
       - Related features to verify
       - Areas that might be affected
     - **Success Criteria**:
       - Checklist mapping to JIRA acceptance criteria
     - **Troubleshooting**:
       - Common issues and debug steps
     - **Notes**:
       - Known limitations
       - Links to JIRA and all PRs
       - Critical test cases highlighted

7. **Exclusions**: Apply smart filtering:
   - **Skip PRs that don't require testing**:
     - PRs that only add documentation (.md files only)
     - PRs that only add CI/tooling (.github/, .claude/ directories)
     - PRs marked with labels like "skip-testing" or "docs-only"
   - **Note skipped PRs** in the test guide with reasoning
   - Focus test scenarios on PRs with actual code changes

8. **Output**: Display the testing guide:
   - Show the file path where the guide was saved
   - Provide a summary of:
     - JIRA issue being tested
     - Number of PRs included
     - Number of test scenarios generated
     - Critical test cases to focus on
   - Highlight any PRs that were skipped and why
   - Ask if the user would like any modifications to the test guide

## Arguments:

- **$1**: JIRA issue key (required) - e.g., CNTRLPLANE-205, OCPBUGS-12345
- **$2, $3, ..., $N**: Optional GitHub PR URLs
  - If provided: Only these PRs will be analyzed
  - If omitted: All PRs linked to the JIRA will be discovered and analyzed

## Smart Features:

1. **Automatic PR Discovery**:
   - Scans JIRA issue for all related PRs
   - Identifies PRs in "Issue Links", "Development" section, and comments
   - Filters to relevant repository (openshift/hypershift)

2. **Selective PR Testing**:
   - Allows manual override to test specific PRs only
   - Useful when JIRA has many PRs but only some need testing

3. **Context-Aware Test Generation**:
   - Bug fixes: Focus on reproduction steps and verification
   - Features: Focus on acceptance criteria and user workflows
   - Refactors: Focus on regression and functional equivalence

4. **Multi-PR Integration**:
   - Understands how multiple PRs work together
   - Creates integration test scenarios
   - Identifies dependencies and testing order

5. **Build/Deploy Section Exclusion**:
   - Does NOT include build or deployment steps
   - Assumes environment is already set up
   - Focuses purely on testing procedures

6. **Cleanup Section Exclusion**:
   - Does NOT include cleanup steps
   - Focuses on test execution and verification

## Example Workflow:

```bash
# Auto-discover all PRs from JIRA
/jira-test CNTRLPLANE-205

# Test only specific PRs
/jira-test CNTRLPLANE-205 https://github.com/openshift/hypershift/pull/6888

# Test multiple specific PRs
/jira-test OCPBUGS-12345 https://github.com/openshift/hypershift/pull/1234 https://github.com/openshift/hypershift/pull/1235
```

The command will provide a comprehensive manual testing guide that QE or developers can use to thoroughly test the JIRA issue implementation.
