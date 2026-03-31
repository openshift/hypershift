# Documentation Preview

When a pull request modifies files under `docs/`, a GitHub Actions workflow automatically builds the documentation and deploys a preview to Cloudflare Pages.

## How It Works

The workflow uses `pull_request_target` with two jobs to securely handle fork PRs:

1. **Build** — checks out the PR code and builds the docs with MkDocs in strict mode. This job has no access to secrets.
2. **Deploy** — downloads the built artifact and deploys it to Cloudflare Pages. This job has access to the `docs-preview` environment secrets but never executes PR code.

GitHub shows a **View deployment** link in the PR timeline via the `docs-preview` environment.

The preview is available at `https://pr-<number>.hypershift.pages.dev`.

## Configuration

The workflow is defined in `.github/workflows/docs-preview.yaml` and runs on self-hosted ARC runners.

It requires two secrets configured on the `docs-preview` GitHub Environment:

| Secret | Description |
|--------|-------------|
| `CLOUDFLARE_ACCOUNT_ID` | Cloudflare account ID |
| `CLOUDFLARE_API_TOKEN` | API token with Cloudflare Pages edit permissions |

## Local Preview

To preview documentation locally:

```bash
cd docs
pip install -r requirements.txt
mkdocs serve
```

Then open [http://127.0.0.1:8000](http://127.0.0.1:8000).
