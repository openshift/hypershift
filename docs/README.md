Any changes to the docs in this directory can be tested locally before pushing changes to GitHub. Just follow these
steps:

1. cd to this directory
2. Run `make build-containerized` to build the docs inside a container
3. Run `make serve-containerized` to serve the docs website locally

Any changes you make while the docs are served locally, will be updated in the local docs website.

## Configuration

The site configuration lives in `mkdocs.yml`. Zensical still uses the MkDocs
configuration format; once Zensical ships a migration tool for its native TOML
config, we will switch over.

## Managing Python dependencies

Dependencies are declared in `pyproject.toml` and locked with
[`uv`](https://docs.astral.sh/uv/) into `uv.lock`, which pins every
direct and transitive package to an exact version with hash verification
for supply chain security. This is what
`.github/workflows/docs-build-reusable.yaml` uses when running
`uv run --frozen zensical build --strict`.

### Adding or updating a dependency

1. Add or update the package in `pyproject.toml`
   (e.g. add `"newplugin>=1.0.0"` to `dependencies`, or bump an existing pin).
2. Regenerate the lockfile:

   ```bash
   uv lock
   ```

3. Confirm the docs still build:

   ```bash
   uv run --frozen zensical build --strict
   ```

