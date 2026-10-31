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

## Assisted-by Footer

If an AI or code assistant helped generate the content of the commit (either the committed files, or the commit message), **include an `Assisted-by` footer**, naming the assistant.

```
Assisted-by: <insert name of code assistant>
```

## Gitlint Validation Rules

- Run `make run-gitlint` to validate commit messages
- **Title line**: 120 characters maximum
- **Body line**: 140 characters maximum per line
- Use conventional commit format
- No trailing whitespace

## Examples

### Simple commit

```
docs: correct spelling of CHANGELOG

Assisted-by: <insert name of code assistant>
```

### With scope

```
feat(azure): add workload identity support

Assisted-by: <insert name of code assistant>
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
Assisted-by: <insert name of code assistant>
```

### Conventional commit examples

Commit message with description and breaking change footer:

```bash
feat: allow provided config object to extend other configs

BREAKING CHANGE: `extends` key in config file is now used for extending other config files
```

Commit message with `!` to draw attention to a breaking change:

```bash
feat!: send an email to the customer when a product is shipped
```

Commit message with scope and `!` to draw attention to a breaking change:

```bash
feat(api)!: send an email to the customer when a product is shipped
```

Commit message with both `!` and a `BREAKING CHANGE` footer:

```bash
chore!: drop support for Node 6

BREAKING CHANGE: use JavaScript features not available in Node 6.
```

Commit message with no body:

```bash
docs: correct spelling of CHANGELOG
```

Commit message with scope:

```bash
feat(lang): add Polish language
```

Commit message with a multi-paragraph body and multiple footers:

```bash
fix: prevent racing of requests

Introduce a request ID and a reference to the latest request. Dismiss
incoming responses other than from the latest request.

Remove timeouts which were used to mitigate the racing issue but are
obsolete now.

Reviewed-by: Z
Refs: #123
```

## Quick Checklist

When creating commits:

- [ ] Use conventional commit format: `<type>(<scope>): <description>`
- [ ] Use an imperative subject and explain "Why" and "How" when appropriate
- [ ] Title under 120 characters
- [ ] Body lines under 140 characters
- [ ] If an AI assistant helped create or generate the commit message, include `Assisted-by`.
- [ ] Validate with `make run-gitlint`
- [ ] Use `!` or `BREAKING CHANGE` for breaking changes

## Reference

Conventional Commits Specification: https://www.conventionalcommits.org/en/v1.0.0/#specification
