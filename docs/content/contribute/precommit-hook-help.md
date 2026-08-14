# General Help on Using precommit Hooks in the HyperShift Repo
precommit hooks are helpful in catching issues prior to any new code or pull request appearing in the HyperShift repo.
The hooks are split into two stages: lightweight commit hooks that run on every commit, and a fast smoke check on push
that catches the most common CI failures. Full verification (staticcheck, linting, full test suite) runs in GitHub
Actions. The following sections walk you through how to install the hooks, what they do, and how to bypass them.

## Installing precommit hooks
Once you have precommit installed on your machine([see this for more info](https://pre-commit.com/#install)), it's quite simple to install the precommit hooks.

```shell
% pre-commit install
pre-commit installed at .git/hooks/pre-commit
pre-commit installed at .git/hooks/pre-push
```

The hooks for each stage are defined in the `.pre-commit-config.yaml` file at the base of the HyperShift repo.

## What runs on commit (pre-commit stage)

These are lightweight checks that run in ~10-30 seconds:

- **check-merge-conflict** — scans for leftover merge conflict markers
- **check-yaml** — validates YAML syntax
- **trailing-whitespace** — strips trailing whitespace
- **verify-codespell** — catches common misspellings
- **cpo-containerfiles-in-sync** — ensures CPO container files stay in sync
- **api-lint-fix** — auto-fixes import ordering in `api/` Go files
- **main-lint-fix** — auto-fixes import ordering in root module Go files
- **run-gitlint** — validates commit messages follow conventional commit format

## What runs on push (pre-push stage)

These are a fast smoke check (~3-8 minutes) designed to catch the most common "this will fail CI" mistakes:

- **make verify-quick** — runs code generation (`go generate`, CRD generation, client generation, docs aggregation) and checks for uncommitted diffs. This catches stale fixtures, mocks, and generated code.
- **make test-changed** — runs unit tests only for Go packages with changes relative to `upstream/main`. Skips entirely if no Go files changed.

Full verification (staticcheck, golangci-lint, go vet, CRD schema checks, etc.) and the complete unit test suite
run in GitHub Actions on your pull request.

## Uninstalling precommit hooks
Sometimes it might be useful to turn off the precommit hooks briefly.

```shell
% pre-commit uninstall
pre-commit uninstalled
pre-push uninstalled
```

## Bypassing precommit hooks
Sometimes you may want to bypass the precommit hooks on a `git push` command, for example, if you just updated something really minor, updating your local `main` branch, or just needed to rerun a `go mod tidy` command, etc. To ignore the `pre-push` hooks, just add the `--no-verify` flag to your command.

```shell
% git push --set-upstream origin remove-autorest --no-verify
% git push -f --no-verify
```

## Python tooling

The hooks depend on Python tools (codespell, gitlint, pyyaml) which are installed into an isolated virtualenv
at `hack/tools/bin/python-venv/`. If [uv](https://docs.astral.sh/uv/) is installed, the Makefile uses it for
faster environment setup; otherwise it falls back to standard `python3 -m venv` + `pip`.
