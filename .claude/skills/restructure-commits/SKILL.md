---
name: restructure-commits
description: >
  Restructure branch commits into logical component-based commits for HyperShift PRs.
  Use when preparing a branch for PR review and the commit history needs to be reorganized
  by architectural component (API, Vendor, CLI, HO, CPO, E2E, Docs). Handles squashing
  WIP commits and creating clean conventional commit history.
---

# Restructure Commits by Component

Reorganize all commits on a feature branch into logical, component-based commits that match
HyperShift's architecture.

## Usage

```
/skill:restructure-commits
```

No arguments required — operates on the current branch and its PR.

## When to Use

- User asks to "redo commits", "restructure commits", "squash by component", or "organize commits"
- Preparing a branch for PR review with clean commit history
- Branch has many small/WIP commits that should be consolidated

## Component Categories and File Mapping

Commits are created in this order. Each commit groups files by architectural boundary.

| Order | Component | Scope | File Patterns |
|-------|-----------|-------|---------------|
| 1 | API | `api` | `api/` (types, deepcopy, CRD manifests, go.mod) — **excluding** `*_test.go` |
| 2 | Vendor | `api` | `vendor/`, `client/`, `cmd/install/assets/hypershift-operator/zz_generated.crd-manifests/` |
| 3 | CLI | `cli` | `cmd/cluster/`, `cmd/install/`, `cmd/nodepool/`, `product-cli/` (source, tests, testdata) |
| 4 | HO | `hypershift-operator` | `hypershift-operator/`, `support/`, `karpenter-operator/`, `kubevirtexternalinfra/`, `pkg/`, `manifests/`, `shared-ingress/`, `sharedingress-config-generator/` |
| 5 | CPO | `control-plane-operator` | `control-plane-operator/`, `control-plane-pki-operator/`, `availability-prober/`, `dnsresolver/`, `etcd-backup/`, `etcd-defrag/`, `etcd-recovery/`, `ignition-server/`, `kas-bootstrap/`, `konnectivity-https-proxy/`, `konnectivity-socks5-proxy/`, `kubernetes-default-proxy/`, `sync-fg-configmap/`, `sync-global-pullsecret/`, `token-minter/` |
| 6 | E2E | `e2e` | `test/`, `api/**/*_test.go` |
| 7 | Docs | `docs` | `docs/`, `examples/` |

**Excluded from restructuring:** `bin/`, `hack/`, `contrib/`, `hypershift-ci-python/`, `self-managed-azure-ci-setup/`, `.claude*/` — include in the most relevant commit based on purpose. When ambiguous, prefer HO.

## Procedure

### 1. Identify merge base and changed files

```bash
BASE_BRANCH=$(gh pr view --json baseRefName -q .baseRefName 2>/dev/null || echo "main")
MERGE_BASE=$(git merge-base ${BASE_BRANCH} HEAD)
git log --oneline ${MERGE_BASE}..HEAD          # review existing commits
git diff ${MERGE_BASE}..HEAD --name-only | sort # all changed files
```

### 2. Reset to merge base (keep changes)

```bash
git reset --soft ${MERGE_BASE}  # keep everything staged
git reset HEAD                   # unstage everything
```

### 3. Stage and commit each component group

For each component (in order), stage matching files and commit:

```bash
# Example: API commit (exclude test files — they belong in E2E)
git add api/ && git reset HEAD 'api/**/*_test.go' 2>/dev/null || true
git commit  # use conventional commit format

# Example: Vendor commit
git add vendor/ client/ \
  "cmd/install/assets/hypershift-operator/zz_generated.crd-manifests/"
git commit

# ... repeat for CLI, HO, CPO, E2E, Docs
```

### 4. Verify and force push

```bash
git status                                  # must be clean
git log --oneline ${BASE_BRANCH}..HEAD      # verify commit structure
git push --force-with-lease                 # requires user confirmation
```

## Commit Message Conventions

### Writing the subject

1. Look at the actual changes in the component to determine what was done
2. Pick the type and scope from the table below
3. Write a concise subject summarizing the *purpose*, not just "update files"
4. Use imperative mood: "add", "update", "remove" — not "added", "adds", "adding"

| Component | Type(Scope) | Example Subject |
|-----------|-------------|-----------------|
| API | `feat(api):` | `feat(api): add FooBar CRD and platform config` |
| Vendor | `chore(api):` | `chore(api): regenerate CRDs, clients, deepcopy, and vendor` |
| CLI | `feat(cli):` | `feat(cli): add --foo-bar flags for cluster creation` |
| HO | `feat(hypershift-operator):` | `feat(hypershift-operator): add FooBar controller` |
| CPO | `feat(control-plane-operator):` | `feat(control-plane-operator): add FooBar controllers` |
| E2E | `test(e2e):` | `test(e2e): add FooBar e2e and validation tests` |
| Docs | `docs:` | `docs: add FooBar documentation and architecture reference` |

### Writing the body

Review staged changes and write a body describing *what* was changed. Use bullet points
for multiple distinct changes. Each line must be under 140 characters (gitlint enforced).

### Notes

- Vendor commit is always `chore(api):` — it's regenerated output
- E2E commit is always `test(e2e):` regardless of what's being tested
- Docs commit has no scope — just `docs:`

## Edge Cases

- **Empty component:** Skip the commit if no files match
- **Support packages:** `support/` goes with HO (commit 4), not CPO
- **Control plane sidecars:** `control-plane-pki-operator/`, `availability-prober/`, `dnsresolver/`, `etcd-*`, `ignition-server/`, `kas-bootstrap/`, `konnectivity-*`, `kubernetes-default-proxy/`, `sync-fg-configmap/`, `sync-global-pullsecret/`, `token-minter/` all go with CPO (commit 5)
- **Shared ingress:** `shared-ingress/`, `sharedingress-config-generator/` go with HO (commit 4)
- **Karpenter operator:** `karpenter-operator/` goes with HO (commit 4)
- **Shared test fixtures in CPO:** `control-plane-operator/**/testdata/` stays with CPO
- **Generated CRD manifests:** `cmd/install/assets/hypershift-operator/zz_generated.crd-manifests/` goes with Vendor (commit 2)
- **API go.mod:** `api/go.mod` goes with API (commit 1), not Vendor
- **API test files:** `api/**/*_test.go` goes with E2E (commit 6), not API
- **Documentation files:** `docs/` and `examples/` go with Docs (commit 7), including `docs/content/reference/api.md`
- **Build/CI tooling:** `hack/`, `contrib/`, etc. — include in most relevant commit, or HO when ambiguous

## Quick Checklist

- [ ] Determined base branch from PR (defaults to `main`)
- [ ] Reviewed all changed files before starting
- [ ] Reset with `--soft` (no data loss)
- [ ] Committed in correct order: API, Vendor, CLI, HO, CPO, E2E, Docs
- [ ] Working tree is clean after all commits
- [ ] Confirmed force push with user before executing
