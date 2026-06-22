---
name: Git Environment
description: "Create development environments with git worktrees, branches, commits, and push to remote. Auto-applies for git workflow tasks."
---

# Git Development Environment Workflow

Automates common git development workflows for HyperShift including worktree creation, branching, committing (with proper format), and pushing to remotes.

## Configuration

Load environment variables from `dev/claude-env.sh`:

```bash
source dev/claude-env.sh
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `GIT_FORK_REMOTE` | `enxebre` | Your fork remote name |
| `GIT_UPSTREAM_REMOTE` | `origin` | Upstream remote (openshift/hypershift) |
| `GIT_BASE_BRANCH` | `main` | Base branch for new features |

## Workflows

### Create New Development Environment

**With worktree (parallel development):**
```bash
# Fetch latest
git fetch $GIT_UPSTREAM_REMOTE $GIT_BASE_BRANCH

# Create worktree with new branch
git worktree add -b <branch-name> <worktree-path> $GIT_UPSTREAM_REMOTE/$GIT_BASE_BRANCH

# Example:
git worktree add -b feat/privatelink-karpenter ../hs-privatelink $GIT_UPSTREAM_REMOTE/$GIT_BASE_BRANCH
```

**Without worktree (same repo):**
```bash
# Fetch latest
git fetch $GIT_UPSTREAM_REMOTE $GIT_BASE_BRANCH

# Create and checkout new branch
git checkout -b <branch-name> $GIT_UPSTREAM_REMOTE/$GIT_BASE_BRANCH

# Example:
git checkout -b fix/bug-123 $GIT_UPSTREAM_REMOTE/$GIT_BASE_BRANCH
```

### Branch Naming Conventions

Use conventional prefixes:
- `feat/<description>` - New features
- `fix/<description>` - Bug fixes
- `docs/<description>` - Documentation
- `refactor/<description>` - Code refactoring
- `test/<description>` - Test additions
- `chore/<description>` - Maintenance tasks

### Commit Changes

**Always follow the git-commit-format skill for commits.**

1. **Stage changes:**
   ```bash
   git add <specific-files>
   ```

2. **Check what's staged:**
   ```bash
   git status
   git diff --cached
   ```

3. **Commit with proper format:**
   ```bash
   git commit -m "$(cat <<'EOF'
   <type>(<scope>): <description>

   [optional body]

   Signed-off-by: <name> <email>
   Commit-Message-Assisted-by: Claude (via Claude Code)
   EOF
   )"
   ```

4. **Validate commit message:**
   ```bash
   make run-gitlint
   ```

### Push to Remote

**First push (set upstream):**
```bash
git push -u $GIT_FORK_REMOTE <branch-name>
```

**Subsequent pushes:**
```bash
git push
```

**Force push (after rebase):**
```bash
# Always use --force-with-lease for safety
git push --force-with-lease
```

### Sync with Upstream

```bash
# Fetch latest from upstream
git fetch $GIT_UPSTREAM_REMOTE $GIT_BASE_BRANCH

# Rebase current branch on base branch
git rebase $GIT_UPSTREAM_REMOTE/$GIT_BASE_BRANCH

# If conflicts, resolve them, then:
git rebase --continue

# Push updated branch (force required after rebase)
git push --force-with-lease
```

### Cleanup Worktree

```bash
# Remove the worktree
git worktree remove <worktree-path>

# Optionally delete the branch
git branch -D <branch-name>

# Prune stale worktree references
git worktree prune
```

### List Worktrees

```bash
git worktree list
```

## Quick Reference Commands

| Task | Command |
|------|---------|
| New worktree + branch | `git worktree add -b <branch> <path> $GIT_UPSTREAM_REMOTE/$GIT_BASE_BRANCH` |
| New branch (no worktree) | `git checkout -b <branch> $GIT_UPSTREAM_REMOTE/$GIT_BASE_BRANCH` |
| Stage all changes | `git add -A` |
| Stage specific files | `git add <file1> <file2>` |
| Check status | `git status` |
| View staged diff | `git diff --cached` |
| Commit | `git commit -m "<message>"` |
| Validate commit | `make run-gitlint` |
| Push (first time) | `git push -u $GIT_FORK_REMOTE <branch>` |
| Push | `git push` |
| Force push (safe) | `git push --force-with-lease` |
| Sync with upstream | `git fetch $GIT_UPSTREAM_REMOTE $GIT_BASE_BRANCH && git rebase $GIT_UPSTREAM_REMOTE/$GIT_BASE_BRANCH` |
| Remove worktree | `git worktree remove <path>` |
| Delete branch | `git branch -D <branch>` |
| List worktrees | `git worktree list` |

## Example Full Workflow

```bash
# 0. Load environment
source dev/claude-env.sh

# 1. Create new feature environment
git fetch $GIT_UPSTREAM_REMOTE $GIT_BASE_BRANCH
git worktree add -b feat/aws-privatelink-subnets ../hs-privatelink $GIT_UPSTREAM_REMOTE/$GIT_BASE_BRANCH
cd ../hs-privatelink

# 2. Make changes...
# ... edit files ...

# 3. Stage and commit
git add -A
git commit -m "$(cat <<'EOF'
feat(aws): add dynamic subnet discovery for privatelink

Implement subnet discovery from Karpenter EC2NodeClass resources
to ensure PrivateLink VPC Endpoints have ENIs in all required
availability zones.

Signed-off-by: Alberto Garcia <agarcia@redhat.com>
Commit-Message-Assisted-by: Claude (via Claude Code)
EOF
)"

# 4. Validate
make run-gitlint

# 5. Push to fork
git push -u $GIT_FORK_REMOTE feat/aws-privatelink-subnets

# 6. Create PR via gh cli
gh pr create --title "feat(aws): add dynamic subnet discovery for privatelink" --body "..."

# 7. After PR merged, cleanup
cd ../hypershift
git worktree remove ../hs-privatelink
git branch -D feat/aws-privatelink-subnets
```

## Notes

- Always use `--force-with-lease` instead of `--force` for safety
- Worktrees allow parallel development on multiple features
- Each worktree shares the same git repository but has its own working directory
- Run `git remote -v` to verify remote configuration
- Configure your fork remote in `dev/claude-env.sh` by setting `GIT_FORK_REMOTE`
