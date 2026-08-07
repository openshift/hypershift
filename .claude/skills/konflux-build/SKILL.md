---
name: konflux-build
description: >
  Create a manual Konflux PipelineRun build from a HyperShift PR with configurable image
  expiry. Use when you need to build a container image from a PR branch for testing, hotfixes,
  or validation. Supports specific component selection and permanent (non-expiring) images.
  Requires oc CLI login to the Konflux cluster.
---

# Create a Manual Konflux Build from a PR

Given a PR and a component name, create a manual PipelineRun that produces a container image.
By default the image expires after 30 days. Use `--non-expiring` for permanent images.

## Usage

```
/skill:konflux-build <pr-number-or-url> [component-name-or-pipeline-file] [--non-expiring]
```

**Arguments:**
- `pr-number-or-url` (required): GitHub PR number or full URL for openshift/hypershift
- `component-name-or-pipeline-file` (optional): Component name (e.g., `hypershift-operator`) or pipeline path (e.g., `.tekton/hypershift-release-mce-26-push.yaml`). If omitted, ask the user which component to build.
- `--non-expiring` (optional): Produce a permanent image instead of a 30-day expiring one

**Examples:**
```
/skill:konflux-build 7813 hypershift-release-mce-26
/skill:konflux-build https://github.com/openshift/hypershift/pull/7813
/skill:konflux-build 7813 hypershift-operator --non-expiring
/skill:konflux-build 7813 .tekton/hypershift-release-mce-26-push.yaml
```

## Steps

### 0. Pre-check: Verify OpenShift Login

1. Run `oc whoami --show-server` — confirm it returns `https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443`. If not, tell the user:
   ```
   oc login https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443
   ```
2. Run `oc project -q` — confirm it returns `crt-redhat-acm-tenant`. If not:
   ```
   oc project crt-redhat-acm-tenant
   ```
   If the switch fails, stop — user lacks namespace access.

### 1. Resolve the PR

```bash
gh pr view <PR> --json headRefOid,headRefName,baseRefName,url
```

Get the commit SHA (`headRefOid`), base branch (`baseRefName`), and PR URL.

### 2. Find the Push Pipeline Template

If the user provided a specific pipeline file path, use it via `git show <baseRef>:<pipeline-file>`.

Otherwise, list `.tekton/` on the base branch for `*-push.yaml` files. Match by component name or let the user pick.

```bash
git show <baseRef>:.tekton/        # list templates
git show <baseRef>:.tekton/<file>  # read chosen template
```

### 3. Generate the PipelineRun YAML

From the push pipeline template:

- Replace `name` with `generateName` (replace `-on-push` with `-manual-push-`)
- Remove all `pipelinesascode.tekton.dev/*` annotations
- Replace template variables:
  - `{{revision}}` → PR's head commit SHA
  - `{{source_url}}` → `https://github.com/openshift/hypershift.git`
  - `{{target_branch}}` → PR's base branch
- **Image expiry:**
  - Default: ensure `image-expires-after` param is `30d`
  - `--non-expiring`: remove `image-expires-after` param entirely
- Replace `{{ git_auth_secret }}` workspace secret with `git-auth-empty`
- Write YAML to `/tmp/<component>-manual-push.yaml`

### 4. Ensure git-auth-empty Secret Exists

```bash
oc get secret git-auth-empty -n crt-redhat-acm-tenant 2>/dev/null || \
oc create secret generic git-auth-empty \
  --type=kubernetes.io/basic-auth \
  --from-literal=username='' \
  --from-literal=password='' \
  -n crt-redhat-acm-tenant
```

### 5. Show YAML and Confirm

Display the generated YAML and ask for confirmation. Indicate whether the image will expire (and when) or be permanent.

### 6. Apply the PipelineRun

```bash
oc create -f /tmp/<component>-manual-push.yaml
```

Report the PipelineRun name.

### 7. Wait for Image and Report Digest

Poll `oc get pipelinerun <name>` every 30 seconds until completion.

If the PipelineRun gets archived before you can check, fall back to `skopeo`:

```bash
skopeo inspect --no-tags docker://<output-image-url>
```

Report the final image reference:
```
quay.io/redhat-user-workloads/crt-redhat-acm-tenant/<component>@sha256:<digest>
```

Show per-architecture digests for multi-arch builds. Remind the user about image expiry status.

## Error Handling

| Scenario | Action |
|----------|--------|
| Not logged in to OpenShift | Show `oc login` command and stop |
| Wrong namespace / no access | Show error and stop |
| PR not found | Show error with PR number |
| No matching push template | List available templates, ask user to pick |
| Multiple component matches | List matches, ask user to pick |
| PipelineRun creation fails | Show error details |
| PipelineRun archived early | Fall back to `skopeo inspect` |

## Requirements

- `oc` CLI logged into the Konflux cluster (`stone-prd-rh01`)
- `gh` CLI authenticated with access to openshift/hypershift
- `skopeo` for image inspection
- Access to the `crt-redhat-acm-tenant` namespace
