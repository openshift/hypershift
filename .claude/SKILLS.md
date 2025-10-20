# HyperShift Claude Skills

This directory contains Claude Code skills that are automatically applied when working on the HyperShift project.

## Available Skills

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
