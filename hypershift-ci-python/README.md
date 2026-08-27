# HyperShift CI Python Image

This directory builds a Python-based CI image used to support `gitlint` and
other Python-based tools in the HyperShift CI pipeline (see `Dockerfile`).

## Managing dependencies

Dependencies are declared in `pyproject.toml` and locked in `uv.lock`. The
Dockerfile installs [`uv`](https://docs.astral.sh/uv/) and uses `uv sync --frozen`
to install the exact locked versions. Python scripts in the image can be invoked
with `uv run --frozen` to run in the locked environment.

### Adding or updating a dependency

1. Edit `pyproject.toml` — add or update the dependency in the `dependencies`
   list. Only direct (top-level) packages need to be listed; transitive
   dependencies are resolved automatically.

2. Regenerate the lock file:

   ```bash
   uv lock --project hypershift-ci-python
   ```

3. Commit both `pyproject.toml` and `uv.lock`.

### Running scripts with the locked environment

```bash
uv run --frozen python your_script.py
```

Run this from the directory containing `pyproject.toml` and `uv.lock`
(i.e., `hypershift-ci-python/`), or from any subdirectory of it.
