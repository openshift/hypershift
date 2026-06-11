---
description: Analyze a JIRA issue and create a pull request to solve it.
---

## Name
jira:solve

## Synopsis
```
/jira:solve <jira-issue-id> [remote] [--ci]
```

## Description

The `jira:solve` command analyzes a JIRA issue and creates a pull request to solve it.

This command takes a JIRA URL, fetches the issue description and requirements, analyzes the codebase to understand how to implement the solution, and creates a comprehensive pull request with the necessary changes.

**Usage Examples:**

1. **Solve a specific JIRA issue**:
   ```
   /jira:solve OCPBUGS-12345 origin
   ```

## Implementation

- The command uses curl to fetch JIRA data via REST API: https://redhat.atlassian.net/rest/api/3/issue/{$1}
- Parses JSON response using jq or text processing
- Extracts key fields: summary, description, components, labels
- Authentication uses Basic auth with JIRA_USERNAME and JIRA_API_TOKEN for Atlassian Cloud
- Creates a PR with the solution

### Process Flow

1. **Issue Analysis**: Parse JIRA URL and fetch issue details:
   - Use curl to fetch JIRA issue data: curl -s -u "$JIRA_USERNAME:$JIRA_API_TOKEN" "https://redhat.atlassian.net/rest/api/3/issue/{$1}"
   - Parse JSON response to extract:
      - Issue summary and description
      - From within the description expect the following sections
         - Required
            - Context
            - Acceptance criteria
         - Optional
            - Steps to reproduce (for bugs)
            - Expected vs actual behavior
   - If `--ci` flag ($3) is NOT set: Ask the user for further issue grooming if the required sections are missing
   - If `--ci` flag ($3) IS set: Proceed with available information, making reasonable assumptions where needed

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
   - Think hard and create a detailed, step-by-step plan to implement this feature. Save it to spec-$1.md within the .work/jira/solve folder, for example .work/jira/solve/spec-OCPBUGS-12345.md
   - If `--ci` flag ($3) is NOT set: Ask the user to review the plan and give them the choice to modify it before starting the implementation
   - If `--ci` flag ($3) IS set: Proceed immediately with implementation without waiting for approval
   - Implement the plan:
    - Make necessary code changes using Edit/MultiEdit tools
    - Follow existing code patterns and conventions
    - Add or update tests when code behavior changes or new functions are introduced
    - Update documentation if needed within the docs/ folder
    - If the problem is too complex consider delegating to one of the SME agents.
    - Ensure godoc comments are generated for any newly created public functions
      - Use your best judgement if godoc comments are needed for private functions
      - For example, a comment should not be generated for a simple function like func add(int a, b) int { return a + b}
    - Create unit tests for any newly created functions
  - After making code changes, you MUST verify the implementation based on the repository's tooling:
    - **Check for Makefile**: Run `ls Makefile` to see if one exists
    - **If Makefile exists**: Check available targets with `make help` or `grep '^[^#]*:' Makefile | head -20`
    - **Run appropriate verification commands**:
      - If `make lint-fix` exists: Run it to ensure imports are sorted and linting issues are fixed
      - If `make verify`, `make build`, `make test` exist: you MUST run these to ensure code builds and passes tests
      - If no Makefile or make targets: Look for alternative commands:
        - Go projects: `go fmt ./...`, `go vet ./...`, `go test ./...`, `go build ./...`
        - Node.js: `npm test`, `npm run build`, `npm run lint`
        - Python: `pytest`, `python -m unittest`, `pylint`, `black .`
        - Other: Follow repository conventions in CI config files (.github/workflows/, .gitlab-ci.yml, etc.)
    - **Never assume make targets exist** - always verify first
    - **If any verification command fails**:
      - Determine whether the failure is caused by your changes or is pre-existing
      - If caused by your changes: fix the issue and re-run the failing command
      - If pre-existing: note the pre-existing failure in the PR description, but still proceed
    - **You MUST NOT proceed to "Commit Creation" until all verification commands have been run and all failures are either fixed or confirmed pre-existing**
    - **Anti-patterns (explicitly forbidden)**:
      - Do NOT run `go test ./changed/package/` instead of `make test` — running only the packages you changed misses cross-package regressions
      - Do NOT skip `make test` because `make lint-fix` had unrelated failures
      - Do NOT assume targeted package tests are "good enough" as a substitute for the full test suite

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
     - Operator changes: Controller logic in `operator/` or `controllers/`
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
     - Always create the PR against the remote origin
     - Use gh cli if you need to

6. **PR Description Review**:
   - After creating the PR, display the PR URL and description to the user
   - If `--ci` flag ($3) is NOT set:
     - Ask the user: "Please review the PR description. Would you like me to update it? (yes/no)"
     - If the user says yes or requests changes:
       - Ask what changes they'd like to make
       - Update the PR description using `gh pr edit {PR_NUMBER} --body "{new_description}"`
       - Repeat this review step until the user is satisfied
     - If the user says no or is satisfied, acknowledge and provide next steps
   - If `--ci` flag ($3) IS set: Skip the review step and proceed to completion


## Arguments:
- $1: The JIRA issue to solve (required)
- $2: The remote repository to push the branch. Defaults to "origin".
- $3: Optional `--ci` flag for non-interactive CI automation mode. When set, skips all user prompts and proceeds automatically.

The command will provide progress updates and create a comprehensive solution addressing all requirements from the JIRA issue.
