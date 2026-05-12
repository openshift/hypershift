# Documentation Preview

When a pull request modifies files under `docs/`, GitHub Actions workflows automatically build the documentation and deploy a preview to Cloudflare Pages.

## How It Works

The preview system uses two separate workflows for security, following the reusable workflow pattern described in [GitHub Actions Workflows](github-actions.md):

1. **Docs Build** (`.github/workflows/docs-build.yaml`) — triggers on `pull_request` for changes under `docs/`. The caller delegates to `docs-build-reusable.yaml@main`, which checks out the PR code, builds with MkDocs in strict mode, and uploads the built site as an artifact. This workflow has no access to secrets.
2. **Docs Deploy** (`.github/workflows/docs-deploy.yaml`) — triggers via `workflow_run` when the Docs Build workflow completes successfully. It downloads the built artifact and deploys to Cloudflare Pages. This workflow has access to the `docs-preview` environment secrets but never executes PR code.

GitHub shows a **View deployment** link in the PR timeline via the `docs-preview` environment.

The preview is available at `https://pr-<number>.hypershift.pages.dev`. Previews are automatically cleaned up by Cloudflare's branch retention policy.

## Configuration

The workflows run on self-hosted ARC runners.

The deploy workflow requires two secrets configured on the `docs-preview` GitHub Environment:

| Secret | Description |
|--------|-------------|
| `CLOUDFLARE_ACCOUNT_ID` | Cloudflare account ID |
| `CLOUDFLARE_API_TOKEN` | API token with Cloudflare Pages edit permissions |

## Troubleshooting

If the preview link doesn't appear on your PR, check that the Docs Build workflow completed successfully. The Docs Deploy workflow triggers automatically after a successful build.

## Local Preview

To preview documentation locally:

```bash
cd docs
pip install -r requirements.txt
mkdocs serve
```

Then open [http://127.0.0.1:8000](http://127.0.0.1:8000).
