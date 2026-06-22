# General Help on Using precommit Hooks in the HyperShift Repo
precommit hooks are helpful in catching issues prior to any new code or pull request appearing in the HyperShift repo. 
In the long run, the precommit hooks will help you save time by catching issues that would normally cause the `verify` 
and `unit` tests fail on your pull request. The following sections will walk you through how to quickly install the 
hooks, quickly uninstall the hooks, and how to bypass the hooks.

## Installing precommit hooks
Once you have precommit installed on your machine([see this for more info](https://pre-commit.com/#install)), it's quite simple to install the precommit hooks.

```shell
% pre-commit install
pre-commit installed at .git/hooks/pre-commit
pre-commit installed at .git/hooks/pre-push
```

The jobs ran at the pre-commit and pre-push stages are defined in the .golangci.yml file at the base of the HyperShift 
repo.

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