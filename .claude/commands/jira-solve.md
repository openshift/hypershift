---
description: Analyze a JIRA issue and create a pull request to solve it.
---

Analyze a JIRA issue and create a pull request to solve it.

[Extended thinking: This command takes a JIRA URL, fetches the issue description and requirements, analyzes the codebase to understand how to implement the solution, and creates a comprehensive pull request with the necessary changes.]

**JIRA Issue Analysis and PR Creation**

## Usage Examples:

1. **Solve a specific JIRA issue**:
   `/jira-solve OCPBUGS-12345 enxebre`

## Implementation Details:

- The command uses curl to fetch JIRA data via REST API: https://issues.redhat.com/rest/api/2/issue/{$1}
- Parses JSON response using jq or text processing
- Extracts key fields: summary, description, components, labels
- No authentication required for public Red Hat JIRA issues
- Creates a PR with the solution

## Process Flow:

1. **Issue Analysis**: Parse JIRA URL and fetch issue details:
   - Use curl to fetch JIRA issue data: curl -s "https://issues.redhat.com/rest/api/2/issue/{$1}"
   - Parse JSON response to extract:
      - Issue summary and description
      - From within the description expect the following sections
         - Required
            - Context
            - Acceptance criteria
         - Optional
            - Steps to reproduce (for bugs)
            - Expected vs actual behavior
   - Ask the user for further issue grooming if the requried sections are missing

2. **Codebase Analysis**: Search and analyze relevant code:
   - Find related files and functions
   - Understand current implementation
   - Identify areas that need changes
   - Use Grep and Glob tools to search for:
      - Related function names mentioned in JIRA
      - File patterns related to the component
      - Similar existing implementations
      - Test files that need updates

3. **Solution Implementation**:
   - Make necessary code changes using Edit/MultiEdit tools
   - Follow existing code patterns and conventions
   - Add or update tests as needed
   - Update documentation if needed within the docs/ folder
   - Ensure code builds and passes existing tests:
      - Use `make pre-commit` if needed, or `make build` and `make test` if you need to be more specific
   - If the problem is too complex consider delegating to one of the SME agents.

4. **PR Creation**: 
   - Create feature branch using the jira-key $1 as the branch name. For example: "git checkout -b fix-{jira-key}"
   - Commit changes with proper commit subject honouring https://www.conventionalcommits.org/en/v1.0.0/ and always include a commit message articulating the "why". For example: `git commit -m"fix(sharedingress): Add missing resources to shared ingress watch" -m"Without this the controller won't reconcile if something out of band manipulates the managed resources".
   - Always push the branch with the changes against the remote specified in argument $2
   - Create pull request with:
     - Clear title referencing JIRA issue as a prefix. For example: "OCPBUGS-12345: ..."
     - The PR description should satisfy the template within .github/PULL_REQUEST_TEMPLATE.md if the file exists
     - The "🤖 Generated with Claude Code" sentence should include a reference to the slash command that triggered the execution, for example "via `/jira-solve OCPBUGS-12345 enxebre`"
     - Always create as draft PR
     - Always create the PR against https://github.com/openshift/hypershift
     - Use gh cli if you need to


## Arguments:
- $1: The JIRA issue to solve (required)
- $2: The remote repository to push the branch. Defaults to "origin".

The command will provide progress updates and create a comprehensive solution addressing all requirements from the JIRA issue.
