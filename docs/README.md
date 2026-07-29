Any changes to the docs in this directory can be tested locally before pushing changes to GitHub. Just follow these
steps:

1. cd to this directory
2. Run `make image` to build the image
3. Run `make build-containerized` to build the containerized version of the image
4. Run `make serve-containerized` to serve up the docs website locally

Any changes you make while the docs are served locally, will be updated in the local docs website.

## Managing Python dependencies

`requirements.txt` pins every Python dependency needed to build the docs
(mkdocs and its plugins) to an exact version and includes
`--hash=sha256:...` entries for each published artifact, enabling pip's
hash-checking mode (`pip install --require-hashes`). This is what
`.github/workflows/docs-build-reusable.yaml` installs before running
`mkdocs build --strict`.

Because hash-checking mode requires every installed package — including
transitive dependencies of mkdocs-material, mkdocs-mermaid2-plugin, and
mkdocs-glightbox — to be pinned and hashed, the file lists the full resolved
dependency closure, not just the three plugins imported directly.

### Adding or updating a dependency

1. Add or update the package's version constraint in `requirements.txt`
   (e.g. `newplugin>=1.0.0`, or bump an existing `==` pin).
2. Regenerate the file with [`uv`](https://docs.astral.sh/uv/), compiling to
   a new temporary path rather than overwriting `requirements.txt` directly,
   then move it into place:

   ```bash
   uv pip compile --generate-hashes docs/requirements.txt -o /tmp/requirements.txt.new
   mv /tmp/requirements.txt.new docs/requirements.txt
   ```

   This resolves the full dependency graph, pins every package (direct and
   transitive) to an exact version, and adds `--hash=sha256:...` entries for
   all of its published wheel/sdist artifacts. Don't edit hashes by hand —
   always regenerate so they're guaranteed to match the published artifact.
3. Validate the result, forcing pip to actually verify every package's hash
   rather than skipping ones already present locally:

   ```bash
   pip install --dry-run --ignore-installed --require-hashes -r docs/requirements.txt
   ```

4. Confirm the docs still build: `cd docs && mkdocs build --strict`
   (after installing the regenerated requirements into your environment).