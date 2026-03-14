---
name: hypershift-backport
description: "HyperShift backport conventions and cherry-pick workflows. Auto-applies when discussing backports, cherry-picks to release branches, or resolving cherry-pick conflicts."
---

# HyperShift Backport Conventions

Applies HyperShift-specific conventions for backporting PRs to release branches.

## Branch Naming

Backport branches follow the pattern:

```
bp<version>/<JIRA-ID>
```

- `<version>`: Short version from target branch, dots removed (e.g., `release-4.21` -> `421`)
- `<JIRA-ID>`: The OCPBUGS issue assigned by Prow (e.g., `OCPBUGS-12345`)

**Examples:**
- `bp421/OCPBUGS-12345` (backport to release-4.21)
- `bp420/OCPBUGS-67890` (backport to release-4.20)

## PR Title Format

Backport PR titles must include the target branch and Jira ID:

```
[release-X.YY] OCPBUGS-XXXXX: <original PR title>
```

## Jira Integration

- Prow creates OCPBUGS issues automatically when `/jira backport <branches>` is posted on the source PR
- Each target branch gets its own OCPBUGS issue
- Issues have dependent bug relationships between branches (e.g., the 4.20 bug depends on the 4.21 bug)
- Multiple branches can be specified comma-separated: `/jira backport release-4.21,release-4.20`

## Cherry-Pick Process

1. **Checkout target release branch** and pull latest with `git pull --rebase <remote> <branch>`
2. **Create backport branch** from the release branch: `git checkout -b bp<ver>/<JIRA-ID>`
3. **Cherry-pick commits in order** from the source PR (preserve original commit order)
4. **Resolve conflicts** if any, then `git add` and `git cherry-pick --continue`
5. **Verify** with `make build`, `make test`, and `make verify`
6. **Push** and create PR against the target release branch

## Conflict Resolution Guidelines

- The target release branch may have diverged significantly from the source
- Preserve the **intent** of the original change while fitting the target branch context
- Some code patterns may differ between releases - adapt accordingly
- After resolving, always verify with `make build`, `make test`, and `make verify`

## Source Branch

Backports do not have to originate from `main`. A PR merged into `release-4.21` can be backported to `release-4.20`. The source branch is whatever branch the original PR was merged into.

## Automatic vs Manual Backports

- **Automatic**: Prow attempts a cherry-pick after `/jira backport`. If no conflicts, it creates the PR automatically.
- **Manual**: If Prow's cherry-pick fails due to conflicts, the backport must be done manually following the process above.
