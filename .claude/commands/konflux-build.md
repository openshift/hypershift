# Create a non-expiring Konflux build from a PR

Given a PR and a component name, create a manual PipelineRun that produces a non-expiring container image.

## Input

- **PR**: $ARGUMENTS (GitHub PR URL or number for openshift/hypershift)
- If no component is specified, ask the user which component to build

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

Use `gh pr view <PR> --json headRefSha,headRefName,baseRefName,url` to get:
- The commit SHA (`headRefSha`)
- The base branch (`baseRefName`) — this determines which push template to use
- The PR URL for reference

### 2. Find the push pipeline template

Look in the `.tekton/` directory of the PR's base branch for `*-push.yaml` files. List the available components and let the user pick if not specified, or if there are multiple matches.

The template lives on the **base branch** of the PR. Use `git show <baseRef>:.tekton/` to list available templates, then `git show <baseRef>:.tekton/<template-file>` to read the chosen one.

### 3. Generate the PipelineRun YAML

Take the push pipeline template and resolve it into a concrete PipelineRun:

- Replace `generateName` with a value based on the template's `name` field, replacing `-on-push` with `-manual-push-`
- Remove all `pipelinesascode.tekton.dev/*` annotations (these are PaC trigger annotations, not needed for manual runs)
- Replace `{{revision}}` and `{{source_url}}` with actual values:
  - `{{revision}}` → the PR's head commit SHA
  - `{{source_url}}` → `https://github.com/openshift/hypershift.git`
  - `{{target_branch}}` → the PR's base branch
- Remove `image-expires-after` param if present (this is what makes it non-expiring)
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

Display the generated YAML and ask for confirmation before applying.

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
