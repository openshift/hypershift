---
name: Git Commit Format
description: "Apply HyperShift conventional commit formatting rules. Use when generating commit messages or creating commits."
---

# Git Commit Message Formatting Rules

Apply conventional commit format for all git commits in the HyperShift project.

## Commit Message Format

```
<type>(<scope>): <description>

[optional body]

[footers]
```

## Commit Types

- **feat**: New features
- **fix**: Bug fixes
- **docs**: Documentation changes
- **style**: Code style changes (formatting, etc.)
- **refactor**: Code refactoring (no functional changes)
- **test**: Adding/updating tests
- **chore**: Maintenance tasks
- **build**: Build system or dependency changes
- **ci**: CI/CD changes
- **perf**: Performance improvements
- **revert**: Revert previous commit

## Breaking Changes

### With ! to draw attention
```
feat!: send email when product shipped
```

### With BREAKING CHANGE footer
```
feat: allow config to extend other configs

BREAKING CHANGE: `extends` key now used for extending config files
```

### Both ! and BREAKING CHANGE
```
chore!: drop support for Node 6

BREAKING CHANGE: use JavaScript features not available in Node 6.
```

## Required Footers

### Signed-off-by Footer

**ALWAYS include `Signed-off-by`** footer with name and email.

Get credentials in this priority order:
1. Environment variables: `$GIT_AUTHOR_NAME` and `$GIT_AUTHOR_EMAIL`
2. Git config: `git config user.name` and `git config user.email`
3. If neither configured, ask user to provide details

### Commit-Message-Assisted-by Footer

**ALWAYS include `Commit-Message-Assisted-by: Claude (via Claude Code)`** when Claude assists with creating or generating the commit message.

```
Commit-Message-Assisted-by: Claude (via Claude Code)
```

## Gitlint Validation Rules

- Run `make run-gitlint` to validate commit messages
- **Title line**: 120 characters maximum
- **Body line**: 140 characters maximum per line
- Use conventional commit format
- Include required footers (Signed-off-by)
- No trailing whitespace

## Examples

### Simple commit
```
docs: correct spelling of CHANGELOG

Signed-off-by: Bryan Cox <brcox@redhat.com>
Commit-Message-Assisted-by: Claude (via Claude Code)
```

### With scope
```
feat(azure): add workload identity support

Signed-off-by: Bryan Cox <brcox@redhat.com>
Commit-Message-Assisted-by: Claude (via Claude Code)
```

### Multi-paragraph with footers
```
fix: prevent racing of requests

Introduce request ID and reference to latest request. Dismiss
incoming responses other than from latest request.

Remove timeouts which were used to mitigate racing but are
obsolete now.

Reviewed-by: Jane Doe
Refs: #123
Signed-off-by: Bryan Cox <brcox@redhat.com>
Commit-Message-Assisted-by: Claude (via Claude Code)
```

## Quick Checklist

When creating commits:
- [ ] Use conventional commit format: `<type>(<scope>): <description>`
- [ ] Title under 120 characters
- [ ] Body lines under 140 characters
- [ ] Include `Signed-off-by` footer
- [ ] Include `Commit-Message-Assisted-by: Claude (via Claude Code)` footer
- [ ] Validate with `make run-gitlint`
- [ ] Use `!` or `BREAKING CHANGE` for breaking changes

## Reference

Conventional Commits Specification: https://www.conventionalcommits.org/en/v1.0.0/#specification
