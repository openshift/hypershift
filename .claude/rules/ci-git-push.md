---
description: Git push rules for CI environments (GitHub Actions workflows)
globs:
alwaysApply: true
---

# Git Push in CI

When running inside GitHub Actions (the `CI` or `GITHUB_ACTIONS` environment variable is set):

1. **NEVER modify the git remote URL.** The `actions/checkout` step configures credentials via `persist-credentials: true`. Running `git remote set-url` destroys this configuration.

2. **NEVER embed tokens in URLs.** Do not use `https://x-access-token:$TOKEN@github.com/...` as a remote URL. The credential helper is already configured.

3. **Just push directly.** Use `git push origin <branch>` — authentication is handled automatically by the credential helper that `actions/checkout` configured.

4. **If a push times out, retry with a longer timeout** (e.g., `timeout 300`). Do not assume a timeout is an authentication failure.

5. **If a push genuinely fails with an auth error**, reconfigure the credential helper using `$PUSH_TOKEN` (not `$GH_TOKEN`):
   ```
   git config --local credential.helper '!f() { echo "username=x-access-token"; echo "password=$PUSH_TOKEN"; }; f'
   ```
   `$PUSH_TOKEN` is the correct token for the PR's fork. `$GH_TOKEN` only has access to the upstream repo.

6. **Use `$PR_BRANCH` for the branch name** when pushing — it is set by the workflow.
