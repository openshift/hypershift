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

## Lifecycle diagrams

The `startup-*.mmd` files in `content/images/cluster-lifecycle/` are the editable
sources for the startup roadmap diagrams. Their SVGs were rendered with Mermaid
11.17.2 using the configuration in each source file. Regenerate the corresponding
SVG when changing a source. The exported width and height are twice the viewBox
size, giving the site viewer room to zoom while keeping SVG text sharp. Preserve
the viewBox and its aspect ratio when setting those intrinsic dimensions.

Use ordinary Markdown images to enable the site's GLightbox viewer. Wrapping an
image in a separate link bypasses that integration.

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
