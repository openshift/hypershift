# check-konflux-triggers

Evaluates Pipelines as Code CEL expressions from `.tekton/*pull-request*` files
against the current branch's changed files to predict which Konflux pipelines
would trigger.

Uses the same [`gobwas/glob`](https://github.com/gobwas/glob) library and
[`google/cel-go`](https://github.com/google/cel-go) engine as
[Pipelines as Code](https://github.com/tektoncd/pipelines-as-code/blob/main/pkg/matcher/cel.go).

## Usage

```bash
# From the repository root:
(cd hack/tools && go run ./check-konflux-triggers/)

# With a custom base ref (defaults to origin/main):
(cd hack/tools && go run ./check-konflux-triggers/ upstream/main)
```

Output is automatically paged through `less` when stdout is a terminal (like
`git` and `systemctl`). Set `$PAGER` to override. When piped, output goes
directly to stdout.

## Example output

```
Changed files (2):
  .github/workflows/lint.yaml
  Dockerfile.github-actions-runner

Pipeline trigger evaluation (event=pull_request, target_branch=main):

  Pipeline                                        Trigger   CEL Expression
  ──────────────────────────────────────────────  ────────  ────────────────────────────────────────
  hypershift-cli-mce-217-on-pull-request          no        event == "pull_request" && ...
▶ hypershift-gh-actions-runner-on-pull-request    YES       event == "pull_request" && ...
  hypershift-operator-main-on-pull-request        no        event == "pull_request" && ...
```
