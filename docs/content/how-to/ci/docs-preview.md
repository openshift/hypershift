# Documentation Preview

When a pull request modifies files under `docs/`, GitHub Actions workflows automatically build the documentation and deploy a preview to Cloudflare Pages.

## How It Works

The preview system uses two separate workflows for security, following the reusable workflow pattern described in [GitHub Actions Workflows](github-actions.md):

1. **Docs Build** (`.github/workflows/docs-build.yaml`) — triggers on `pull_request` for changes under `docs/` or to `docs-build.yaml` itself. The caller delegates to `docs-build-reusable.yaml@main`, which checks out the PR code, builds with Zensical in strict mode, and uploads the built site as an artifact. This workflow has no access to secrets.
2. **Docs Deploy** (`.github/workflows/docs-deploy.yaml`) — triggers via `workflow_run` when the Docs Build workflow completes successfully. It downloads the built artifact and deploys to Cloudflare Pages. This workflow has access to the `docs-preview` environment secrets but never executes PR code.

GitHub shows a **View deployment** link in the PR timeline via the `docs-preview` environment.

The preview is available at `https://pr-<number>.hypershift.pages.dev`.

## Production Publishing

The production site at [https://hypershift.pages.dev/](https://hypershift.pages.dev/) is deployed by a third workflow, **Docs Publish** (`.github/workflows/docs-publish.yaml`). It triggers on `push` to `main` when `docs/**` changes or when one of its own workflow files changes (`docs-publish.yaml`, `docs-build-reusable.yaml`, `docs-deploy-reusable.yaml`), and it can also be started manually with `workflow_dispatch` — which is not restricted to `main`. Unlike the preview flow it **builds and deploys inside a single workflow**, so no artifact crosses a `workflow_run` boundary:

- `build` job → `docs-build-reusable.yaml@main`
- `deploy` job (`needs: build`, `production: true`) → `docs-deploy-reusable.yaml@main`

The deploy runs in the `docs-production` environment under a `docs-production` concurrency group with `cancel-in-progress: true`, which cancels an older run in the group as soon as a newer one starts. That converges a burst of merges on the newest commit, but it is not a strict ordering guarantee: cancellation stops the workflow, while a Cloudflare deploy that an older run had already started can still complete.

!!! note
    `docs-deploy-reusable.yaml` supports both production (`branch=main`) and preview (`branch=<name>`) modes, but publishing only uses the production mode. PR previews still go through the older inline implementation in `docs-deploy.yaml`.

!!! warning "Manual runs publish the checked-out ref"
    The production step always runs `wrangler pages deploy ... --branch=main`, so whatever ref the workflow checked out becomes the content of `hypershift.pages.dev` — **including a manual run started on a feature branch**. Use `gh workflow run docs-publish.yaml --ref main` for a real production publish.

## Configuration

The workflows run on self-hosted ARC runners.

The deploy workflow requires two secrets configured on the `docs-preview` GitHub Environment:

| Secret | Description |
|--------|-------------|
| `CLOUDFLARE_ACCOUNT_ID` | Cloudflare account ID |
| `CLOUDFLARE_API_TOKEN` | API token with Cloudflare Pages edit permissions |

The same two secrets must also exist on the `docs-production` Environment, otherwise the production deploy in **Docs Publish** fails. See [GitHub Actions Workflows](github-actions.md) for the full trigger graph.

## Local Preview

To preview documentation locally:

```bash
cd docs
uv run zensical serve
```

Then open [http://127.0.0.1:8000](http://127.0.0.1:8000).
