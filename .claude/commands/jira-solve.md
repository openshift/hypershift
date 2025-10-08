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
   - If the problem is too complex consider delegating to one of the SME agents.
   - Ensure godoc comments are generated for any newly created public functions
      - Use your best judgement if godoc comments are needed for private functions
      - For example, a comment should not be generated for a simple function like func add(int a, b) int { return a + b}
   - Create unit tests for any newly created functions
   - After making code changes, always run `make lint-fix` to ensure imports are properly sorted and linting issues are fixed
   - Always ensure code builds and passes existing tests:
   - Run `make pre-commit`, or `make verify`, `make build` and `make test` if you need to be more specific
   - Do not go to the "Commit Creation" step until `make pre-commit` passes

4. **Commit Creation**: 
   - Create feature branch using the jira-key $1 as the branch name. For example: "git checkout -b fix-{jira-key}"
   - Break commits into logical components based on the nature of the changes
   - Each commit should honor https://www.conventionalcommits.org/en/v1.0.0/ and always include a commit message body articulating the "why"
   - Use your judgment to organize commits in a way that makes them easy to review and understand
   - Common logical groupings (use as guidance, not rigid rules):
     - API changes: Changes in `api/` directory (types, CRDs)
       - Example: `git commit -m"feat(api): Update HostedCluster API for X" -m"Add new fields to support Y functionality"`
     - Vendor changes: Dependency updates in `vendor/` directory
       - Example: `git commit -m"chore(vendor): Update dependencies for X" -m"Required to pick up bug fixes in upstream library Y"`
     - Generated code: Auto-generated clients, informers, listers, and CRDs
       - Example: `git commit -m"chore(generated): Regenerate clients and CRDs" -m"Regenerate after API changes to ensure client code is in sync"`
     - CLI changes: User-facing command changes in `cmd/` directory
       - Example: `git commit -m"feat(cli): Add support for X flag" -m"This allows users to configure Y behavior at cluster creation time"`
     - Operator changes: Controller logic in `hypershift-operator/` or `control-plane-operator/`
       - Example: `git commit -m"feat(operator): Implement X controller logic" -m"Without this the controller won't reconcile when Y condition occurs"`
     - Support/utilities: Shared code in `support/` directory
       - Example: `git commit -m"refactor(support): Extract common X utility" -m"Consolidate duplicated logic from multiple controllers into shared helper"`
     - Tests: Test additions or modifications
       - Example: `git commit -m"test: Add tests for X functionality" -m"Ensure the new behavior is covered by unit tests to prevent regressions"`
     - Documentation: Changes in `docs/` directory
       - Example: `git commit -m"docs: Document X feature" -m"Help users understand how to configure and use the new capability"`

5. **PR Creation**: 
   - Push the branch with all commits against the remote specified in argument $2
   - Create pull request with:
     - Clear title referencing JIRA issue as a prefix. For example: "OCPBUGS-12345: ..."
     - The PR description should satisfy the template within .github/PULL_REQUEST_TEMPLATE.md if the file exists
     - The "🤖 Generated with Claude Code" sentence should include a reference to the slash command that triggered the execution, for example "via `/jira-solve OCPBUGS-12345 enxebre`"
     - Always create as draft PR
     - Always create the PR against https://github.com/openshift/hypershift
     - Use gh cli if you need to

6. **PR Description Review**:
   - After creating the PR, display the PR URL and description to the user
   - Ask the user: "Please review the PR description. Would you like me to update it? (yes/no)"
   - If the user says yes or requests changes:
     - Ask what changes they'd like to make
     - Update the PR description using `gh pr edit {PR_NUMBER} --body "{new_description}"`
     - Repeat this review step until the user is satisfied
   - If the user says no or is satisfied, acknowledge and provide next steps


## Arguments:
- $1: The JIRA issue to solve (required)
- $2: The remote repository to push the branch. Defaults to "origin".

The command will provide progress updates and create a comprehensive solution addressing all requirements from the JIRA issue.
