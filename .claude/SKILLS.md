# HyperShift Claude Skills

This directory contains Claude Code skills that are automatically applied when working on the HyperShift project.

## Available Skills

### Code Formatting

**Location:** `.claude/skills/code-formatting/`

**Description:** Applies HyperShift code quality, formatting and testing conventions.

**Auto-applies when:**
- Writing Go code
- Creating unit tests
- Preparing commits

**Covers:**
- Running `make lint-fix` after writing Go code
- Running `make verify` before committing
- Using `make verify-codespell` for markdown
- "When...it should..." test naming convention
- Including unit tests with new functions

**Benefits:**
- Ensures code passes linting before commits
- Enforces consistent test naming
- Catches spelling errors in documentation

### Effective Go

**Location:** `.claude/skills/effective-go/`

**Description:** Automatically applies Go best practices and idioms from [golang.org/doc/effective_go](https://go.dev/doc/effective_go) when writing or reviewing Go code.

**Auto-applies when:**
- Writing new Go code
- Reviewing or refactoring existing Go code
- Debugging Go-specific issues
- Discussing Go best practices

**Covers:**
- Formatting and code style (gofmt)
- Naming conventions (packages, interfaces, functions)
- Control structures and error handling
- Concurrency patterns (goroutines, channels, select)
- Interface design principles
- Common anti-patterns to avoid

**Benefits:**
- Ensures consistent, idiomatic Go code across the project
- Catches common mistakes during development
- Promotes best practices for concurrency and error handling
- Provides quick reference during code reviews

## How Skills Work

Skills are automatically invoked by Claude based on context. You don't need to do anything special - just ask Claude to work with Go code, and the Effective Go guidelines will be applied automatically.

## Adding New Skills

To add a new skill:
1. Create a directory: `.claude/skills/your-skill-name/`
2. Add a `SKILL.md` file with YAML frontmatter
3. Commit to the repository for team-wide availability

See [Claude Code Skills Documentation](https://docs.claude.com/en/docs/claude-code/skills) for details.
