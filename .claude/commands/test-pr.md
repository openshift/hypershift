---
description: Generate manual test steps for one or more related PRs
---

Generate manual test steps for one or more related PRs.

[Extended thinking: This command takes one or more GitHub PR URLs, fetches the PR details including description, commits, and file changes, analyzes the changes to understand what features/fixes were implemented, and generates a comprehensive manual testing guide with step-by-step instructions. When multiple PRs are provided, it analyzes them collectively to understand how they work together to fix a bug or implement a feature.]

**PR Testing Guide Generator**

## Usage Examples:

1. **Generate test steps for a single PR**:
   `/test-pr https://github.com/openshift/hypershift/pull/6888`

2. **Generate test steps for multiple related PRs**:
   `/test-pr https://github.com/openshift/hypershift/pull/6888 https://github.com/openshift/hypershift/pull/6889 https://github.com/openshift/hypershift/pull/6890`

## Implementation Details:

- The command uses `gh pr view` to fetch PR data for one or more PRs
- Analyzes PR descriptions, commits, and changed files across all provided PRs
- Identifies relationships and dependencies between multiple PRs
- Generates test scenarios based on the collective changes
- Creates a comprehensive testing guide with prerequisites and verification steps

## Process Flow:

1. **PR Analysis**: Parse GitHub URLs and fetch PR details:
   - Extract PR numbers from all provided URLs (supports multiple PRs)
   - For each PR, use `gh pr view {PR_NUMBER} --json title,body,commits,files,labels` to fetch:
     - PR title and description
     - Commit messages and history
     - Changed files and their diffs
     - PR labels (bug, enhancement, etc.)
   - Read the changed files to understand the implementation
   - When multiple PRs are provided:
     - Identify common JIRA issues or bug references across PRs
     - Analyze how changes in different PRs complement each other
     - Determine the order in which PRs should be tested (if dependencies exist)

2. **Change Analysis**: Understand what was changed:
   - For single PR:
     - Identify the type of change (feature, bug fix, refactor, etc.)
     - Determine affected components (API, CLI, operator, control-plane, etc.)
     - Find related platform-specific changes (AWS, Azure, KubeVirt, etc.)
   - For multiple PRs:
     - Identify the overall objective (complete bug fix, multi-component feature, etc.)
     - Map which PR addresses which component or aspect of the fix
     - Identify overlapping or complementary changes
     - Determine if PRs target different repositories or components
   - Review test files to understand expected behavior
   - Use Grep and Glob tools to:
     - Find related configuration or documentation
     - Locate example usage in existing tests
     - Identify dependencies or related features

3. **Test Scenario Generation**: Create comprehensive test plan:
   - Analyze the PR description(s) for:
     - Feature requirements and acceptance criteria
     - Bug reproduction steps
     - Related JIRA issues or issue references
   - For multiple PRs, create integrated test scenarios that:
     - Test the complete fix/feature with all PRs applied
     - Verify each PR's contribution to the overall solution
     - Ensure PRs work correctly together without conflicts
   - Generate test scenarios covering:
     - Happy path scenarios
     - Edge cases and error handling
     - Platform-specific variations if applicable
     - Upgrade/downgrade scenarios if relevant
     - Performance impact if significant changes

4. **Test Guide Creation**: Create detailed manual testing document:
   - For single PR: Save to `test-pr-{PR_NUMBER}.md`
   - For multiple PRs: Save to `test-pr-{PR_NUMBER1}-{PR_NUMBER2}-{PR_NUMBERN}.md` (e.g., `test-pr-6888-6889-6890.md`)
   - Include the following sections:
     - **PR Summary**:
       - For single PR: Title, description, and key changes
       - For multiple PRs: List all PRs with their titles, show the common objective, and explain how they work together
     - **Prerequisites**:
       - Required infrastructure (AWS account, S3 bucket, etc.)
       - Tools and CLI versions needed
       - Environment setup steps
     - **Test Scenarios**:
       - Numbered test cases with clear steps
       - Expected results for each step
       - Verification commands and their expected output
       - For multiple PRs: Include integration test scenarios
     - **Regression Testing**:
       - Suggestions for related features to verify
     - **Notes**:
       - Known limitations or areas requiring special attention
       - Links to related PRs or documentation
       - For multiple PRs: Dependencies between PRs and recommended testing order

5. **Output**: Display the testing guide:
   - Show the file path where the guide was saved
   - Provide a brief summary of the test scenarios
   - Highlight any critical test cases or prerequisites
   - Ask if the user would like any modifications to the test guide

## Arguments:
- $1, $2, $3, ..., $N: One or more GitHub PR URLs (at least one required)
  - Single PR: `/test-pr https://github.com/openshift/hypershift/pull/6888`
  - Multiple PRs: `/test-pr https://github.com/openshift/hypershift/pull/6888 https://github.com/openshift/hypershift/pull/6889`

The command will provide a comprehensive manual testing guide that can be used by QE or developers to thoroughly test the PR changes. When multiple PRs are provided, the guide will include integrated test scenarios that verify the PRs work correctly together.
