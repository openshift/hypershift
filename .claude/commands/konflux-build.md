---
description: Create a manual Konflux build from a PR with configurable image expiry (default 30 days)
argument-hint: "<PR-number-or-URL> [component-name or pipeline-file] [--non-expiring]"
---

# Create a manual Konflux build from a PR

Given a PR and a component name, create a manual PipelineRun that produces a container image. By default the image expires after 30 days. Use `--non-expiring` to produce a permanent image.

## Usage Examples

1. **Build a specific component from a PR number** (expires in 30 days):
   `/konflux-build 7813 hypershift-release-mce-26`

2. **Build from a PR URL** (will prompt for component):
   `/konflux-build https://github.com/openshift/hypershift/pull/7813`

3. **Build a non-expiring image for a hotfix**:
   `/konflux-build 7813 hypershift-operator --non-expiring`

4. **Build using a specific pipeline template**:
   `/konflux-build 7813 .tekton/hypershift-release-mce-26-push.yaml`

5. **Build the main operator from a PR**:
   `/konflux-build 7500 hypershift-operator`

## What This Command Does

1. Verifies you are logged into the Konflux cluster (`stone-prd-rh01`)
2. Resolves the PR to get its head commit SHA and base branch
3. Finds the matching push pipeline template from `.tekton/` on the base branch
4. Generates a manual PipelineRun YAML with template variables resolved
5. Sets image expiry to 30 days (or removes it if `--non-expiring` is specified)
6. Creates the PipelineRun and polls until completion
7. Reports the final image reference with `@sha256:` digest

## Input

- **PR**: $ARGUMENTS (GitHub PR URL or number for openshift/hypershift)
- **Component or pipeline file**: either a component name (e.g., `hypershift-operator`) or a path to a specific pipeline template (e.g., `.tekton/hypershift-release-mce-26-push.yaml`). If not specified, ask the user which component to build.
- If `--non-expiring` is present in the arguments, produce a permanent image; otherwise set `image-expires-after: 30d`

## Steps

### 0. Pre-check: verify OpenShift login

Before doing anything else, verify the user is logged in to the correct cluster and project:

1. Run `oc whoami --show-server` and confirm it returns `https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443`. If not, stop and tell the user to log in:
   ```
   oc login https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443
   ```
2. Run `oc project -q` and confirm it returns `crt-redhat-acm-tenant`. If not, switch to it:
   ```
   oc project crt-redhat-acm-tenant
   ```
   If the switch fails, stop and tell the user they don't have access to the required namespace.

Only proceed to the next steps once both checks pass.

### 1. Resolve the PR

Use `gh pr view <PR> --json headRefOid,headRefName,baseRefName,url` to get:
- The commit SHA (`headRefOid`)
- The base branch (`baseRefName`) — this determines which push template to use
- The PR URL for reference

### 2. Find the push pipeline template

If the user provided a specific pipeline file path (e.g., `.tekton/hypershift-release-mce-26-push.yaml`), use that template directly via `git show <baseRef>:<pipeline-file>`.

Otherwise, look in the `.tekton/` directory of the PR's base branch for `*-push.yaml` files. Match by component name if provided, or list the available components and let the user pick.

The template lives on the **base branch** of the PR. Use `git show <baseRef>:.tekton/` to list available templates, then `git show <baseRef>:.tekton/<template-file>` to read the chosen one.

### 3. Generate the PipelineRun YAML

Take the push pipeline template and resolve it into a concrete PipelineRun:

- Replace `name` with `generateName` based on the template's `name` field, replacing `-on-push` with `-manual-push-`
- Remove all `pipelinesascode.tekton.dev/*` annotations (these are PaC trigger annotations, not needed for manual runs)
- Replace `{{revision}}` and `{{source_url}}` with actual values:
  - `{{revision}}` → the PR's head commit SHA
  - `{{source_url}}` → `https://github.com/openshift/hypershift.git`
  - `{{target_branch}}` → the PR's base branch
- **Image expiry handling:**
  - If `--non-expiring` was specified: remove `image-expires-after` param entirely
  - Otherwise: ensure an `image-expires-after` param is present with value `30d` (add it if the template doesn't have one, or update it if it does)
- Replace the git-auth workspace secret reference (`{{ git_auth_secret }}`) with `git-auth-empty`
- If `pipelineRef` uses a `resolver: git`, keep it as-is. If it uses `name:`, keep it as-is.
- Write the YAML to `/tmp/<component>-manual-push.yaml`

### 4. Ensure the git-auth-empty secret exists

The git-clone Tekton task requires the `git-auth` workspace to be a `kubernetes.io/basic-auth` typed secret. Since openshift/hypershift is a public repo, we use an empty secret instead of real credentials (which would be visible to all tenant users):

```bash
oc get secret git-auth-empty -n crt-redhat-acm-tenant 2>/dev/null || \
oc create secret generic git-auth-empty \
  --type=kubernetes.io/basic-auth \
  --from-literal=username='' \
  --from-literal=password='' \
  -n crt-redhat-acm-tenant
```

### 5. Show the user the PipelineRun YAML and confirm

Display the generated YAML and ask for confirmation before applying. Clearly indicate whether the image will expire (and when) or be permanent.

### 6. Apply the PipelineRun

```bash
oc create -f /tmp/<component>-manual-push.yaml
```

Report the PipelineRun name.

### 7. Wait for the image and report the digest

Poll `oc get pipelinerun <name>` every 30 seconds until it completes or disappears (archived).

Once done (or if the PipelineRun gets archived before we can check), use `skopeo` to get the image digest:

```bash
skopeo inspect --no-tags docker://<output-image-url>
```

Report the final image reference in `@sha256:` digest form, e.g.:
```
quay.io/redhat-user-workloads/crt-redhat-acm-tenant/<component>@sha256:<digest>
```

Also show per-architecture digests from the manifest list if it's a multi-arch build.

Remind the user whether the image expires (and when) or is permanent.

## Error Handling

| Scenario | Action |
|----------|--------|
| Not logged in to OpenShift | Show `oc login` command and stop |
| Wrong namespace / no access | Show error and stop |
| PR not found | Show error with PR number |
| No matching push template | List available templates and ask user to pick |
| Multiple component matches | List matches and ask user to pick |
| PipelineRun creation fails | Show error details |
| PipelineRun archived before completion | Fall back to `skopeo inspect` to check the image directly |

## Requirements

- `oc` CLI logged into the Konflux cluster
- `gh` CLI authenticated with access to openshift/hypershift
- `skopeo` for image inspection
- Access to the `crt-redhat-acm-tenant` namespace
