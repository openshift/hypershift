Upgrade any Go module dependency to latest stable version and update codebase to follow new best practices:

[Extended thinking: This workflow automates the complex process of upgrading any Go dependency, analyzing breaking changes, updating code patterns, and creating a comprehensive PR with all changes and changelog.]

Usage: /workflows:dependency-upgrade <dependency-name>

Examples:
- /workflows:dependency-upgrade sigs.k8s.io/controller-runtime
- /workflows:dependency-upgrade k8s.io/client-go
- /workflows:dependency-upgrade github.com/prometheus/client_golang

Use the Task tool to delegate to the specialized agent:

1. **Dependency upgrade analysis and implementation**
   - Use Task tool with subagent_type="go-mod-upgrades-sme"
   - Prompt: "Upgrade Go dependency '$ARGUMENTS' in HyperShift to latest stable version. Follow this comprehensive workflow: 1) Analyze current version in go.mod vs latest GitHub release 2) Check for related dependencies that should be upgraded together 3) Update to latest stable using go mod commands 4) Analyze breaking changes and update all consumption patterns 5) Apply new best practices throughout codebase 6) Run 'make build' to verify build compatibility (required success criteria) 7) Run tests to verify functionality 8) Handle vendor directory if used 9) Create PR with detailed changelog between versions. Include full analysis of changes, migration details, and rollback plan if needed. Build success criteria: 'make build' must complete without errors."

This workflow will:
- Compare current vs latest dependency versions
- Update go.mod and resolve dependency conflicts
- Identify and fix breaking changes
- Update code to use new best practices
- Validate with make build (required success criteria)
- Test functionality and compatibility
- Generate comprehensive PR with changelog
- Provide rollback instructions if upgrade fails