# Sync Community Fork

A GitHub Actions workflow automatically pushes every commit on `main` to the [hypershift-community/hypershift](https://github.com/hypershift-community/hypershift) fork.

## How It Works

The workflow is defined in `.github/workflows/sync-community-fork.yaml`. On every push to `main` it checks out the repository using a fine-grained Personal Access Token (PAT) and runs `git push` to the community fork. The PAT is used instead of the default `GITHUB_TOKEN` because the latter only has access to the source repository.

## Configuration

The workflow requires one secret configured at the repository level:

| Secret | Description |
|--------|-------------|
| `COMMUNITY_FORK_TOKEN` | Fine-grained GitHub PAT with push access to `hypershift-community/hypershift` |

### Creating the Token

1. Go to **Settings > Developer settings > Personal access tokens > Fine-grained tokens**.
2. Click **Generate new token**.
3. Set **Resource owner** to the `hypershift-community` organization.
4. Under **Repository access**, select **Only select repositories** and choose `hypershift-community/hypershift`.
5. Grant **no organization permissions**.
6. Grant the following **repository permissions**:
   - Metadata — **Read**
   - Contents — **Read and write**
   - Pull requests — **Read and write**
   - Workflows — **Read and write**
7. Click **Generate token** and copy the value.

### Rotating the Token

1. Create a new token following the steps above.
2. Update the repository secret using one of the following options:

   **Option A — GitHub CLI:**

   ```bash
   gh secret set COMMUNITY_FORK_TOKEN --repo openshift/hypershift
   ```

   This will prompt you to paste the new token value.

   **Option B — Web UI:**

   In the `openshift/hypershift` repository, go to **Settings > Secrets and variables > Actions** and update the `COMMUNITY_FORK_TOKEN` secret with the new token value.

3. Verify the workflow runs successfully on the next push to `main`.
4. Delete the old token from your GitHub account.
