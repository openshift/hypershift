# HyperShift CI Python Image

This directory builds a Python-based CI image used to support `gitlint` and
other Python-based tools in the HyperShift CI pipeline (see `Dockerfile`).

## Managing dependencies

`requirements.txt` pins every dependency to an exact version and includes
`--hash=sha256:...` entries for each published artifact. This enables pip's
hash-checking mode (`pip install --require-hashes`), which verifies that the
downloaded package matches the artifact originally published to PyPI —
protection against a compromised or tampered package matching a version pin.

Because hash-checking mode requires **every** installed package, including
transitive dependencies, to be pinned and hashed, `requirements.txt` lists the
full resolved dependency closure, not just the packages imported directly.

### Adding a new dependency

1. Add the new package to `requirements.txt` with any version constraint
   (e.g. `newpackage>=1.0.0`), or leave it unpinned to pick up the latest
   version.
2. Regenerate the file with [`uv`](https://docs.astral.sh/uv/):

   ```bash
   uv pip compile --generate-hashes hypershift-ci-python/requirements.txt \
     -o hypershift-ci-python/requirements.txt
   ```

   This resolves the full dependency graph, pins every package (direct and
   transitive) to an exact version, and adds `--hash=sha256:...` entries for
   all of its published wheel/sdist artifacts.

### Updating an existing dependency's hash

If a package needs to be bumped to a new version, update its version
constraint in `requirements.txt` first (or just re-run the compile step if
you want to pick up the latest version satisfying the existing constraint),
then regenerate as above. Manually editing a hash is discouraged — always
regenerate via `uv pip compile --generate-hashes` so the hash is guaranteed
to match the actual published artifact.

### Validating the result

Before committing, confirm the file installs cleanly under hash-checking
mode:

```bash
pip install --dry-run --require-hashes -r hypershift-ci-python/requirements.txt
```

`pip-compile` (from `pip-tools`) is an equivalent alternative to `uv pip
compile` if you don't have `uv` installed.
