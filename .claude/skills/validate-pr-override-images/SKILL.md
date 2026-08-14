---
description: Validates that CPO override images in a PR actually contain the PRs they claim to include
argument-hint: "<PR-URL-or-number>"
---

## Name
validate-pr-override-images

## Synopsis
```text
/validate-pr-override-images <PR-URL-or-number>
```

## Description
Validates that CPO override images in a PR actually contain the claimed fix PRs.

The PR description must include a structured validation contract as documented
in [docs/content/contribute/cpo-overrides.md](../../docs/content/contribute/cpo-overrides.md)
under "Validating Override Images Contain Claimed PRs". Example:
```
branch: 4.20 wants: https://github.com/openshift/hypershift/pull/8593
branch: 4.21 wants: https://github.com/openshift/hypershift/pull/8593, https://github.com/openshift/hypershift/pull/8565
```

Prerequisites:
- `skopeo` must be installed (`brew install skopeo` on macOS)
- The local git repo must have the relevant release branches fetched
- Images must be accessible from quay.io

## Implementation

Extract the PR number from the argument, then run:
```bash
.claude/skills/validate-pr-override-images/validate-overrides.sh <pr-number>
```

Report the output to the user.

## Arguments
- `$1`: PR URL (e.g., `https://github.com/openshift/hypershift/pull/8610`) or PR number (e.g., `8610`)
