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

To add a new dependency or update an existing one:

1. Add the new package to `requirements.txt` with any version constraint
   (e.g. `newpackage>=1.0.0`), or leave it unpinned to pick up the latest
   version.
2. Regenerate the file with [`uv`](https://docs.astral.sh/uv/), compiling to
   a **new temporary path** rather than overwriting `requirements.txt`
   directly, then move it into place:

   ```bash
   uv pip compile --generate-hashes hypershift-ci-python/requirements.txt \
     -o /tmp/requirements.txt.new
   mv /tmp/requirements.txt.new hypershift-ci-python/requirements.txt
   ```

   This resolves the full dependency graph, pins every package (direct and
   transitive) to an exact version, and adds `--hash=sha256:...` entries for
   all of its published wheel/sdist artifacts.
3. Validate, forcing pip to actually verify every package's hash rather
   than skipping ones already present locally:
   ```bash
   pip install --dry-run --ignore-installed --require-hashes -r hypershift-ci-python/requirements.txt
   ```