# CPO Overrides Configuration Guide

## Overview

The CPO (Control Plane Operator) overrides mechanism allows you to specify custom Control Plane Operator images for specific OpenShift versions and platforms. This is particularly useful for applying hotfixes and patches before they're officially released.

Override versions can be exact strings or glob patterns, so `4.20.15-*` covers
every payload built from 4.20.15 with a suffix without naming the (possibly
private) build string. See [Version Matching](#version-matching).

## Configuration File Structure

The CPO overrides are configured using a YAML file with the following structure:

```yaml
platforms:
  aws:
    overrides:
      - version: "4.17.9"
        cpoImage: "quay.io/hypershift/control-plane-operator:4.17.9"
      - version: "4.17.8"
        cpoImage: "quay.io/hypershift/control-plane-operator:4.17.8"
    testing:
      latest: "quay.io/openshift-release-dev/ocp-release:4.17.9-x86_64"
      previous: "quay.io/openshift-release-dev/ocp-release:4.17.8-x86_64"
  azure:
    overrides:
      - version: "4.16.17"
        cpoImage: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:1a50894aafa6b750bf890ef147a20699ff5b807e586d15506426a8a615580797"
      - version: "4.16.18"
        cpoImage: "quay.io/hypershift/hypershift-cpo:patch"
```

## Configuration Elements

### Platforms Section

The top-level `platforms` section contains platform-specific configurations. Currently supported platforms:

- **`aws`**: Amazon Web Services
- **`azure`**: Microsoft Azure

Each platform section is optional. If a platform is not configured, the default CPO image from the OpenShift release will be used.

### Platform Configuration

Each platform can contain:

#### `overrides` (Array)
A list of version-specific CPO image overrides:

- **`version`** (string): The OpenShift version the override applies to. Either an exact version string (`"4.17.9"`, or a full payload version such as `"4.17.9-hotfix34123420"`), or a glob pattern (`"4.17.9-*"`), which matches every payload whose version matches the pattern. See [Version Matching](#version-matching).
- **`cpoImage`** (string): The full container image reference to use for this version

#### `testing` (Object, Optional)
Configuration for automated testing:

- **`latest`** (string): The latest OpenShift release image for testing
- **`previous`** (string): The previous OpenShift release image for testing
- **`runTests`** (boolean): Whether to run override tests for this platform when TEST_CPO_OVERRIDE=1 is set. Only one platform should have this set to `true` at a time

## Configuration Examples

### Basic Platform-Specific Override

```yaml
platforms:
  aws:
    overrides:
      - version: "4.17.9"
        cpoImage: "quay.io/myorg/custom-cpo-aws:4.17.9"
```

### Multiple Versions and Platforms

```yaml
platforms:
  aws:
    overrides:
      - version: "4.17.9"
        cpoImage: "quay.io/hypershift/control-plane-operator-aws:4.17.9"
      - version: "4.17.8"
        cpoImage: "quay.io/hypershift/control-plane-operator-aws:4.17.8"
      - version: "4.16.15"
        cpoImage: "quay.io/hotfix/cpo-aws:4.16.15-hotfix"
    testing:
      latest: "quay.io/openshift-release-dev/ocp-release:4.17.9-x86_64"
      previous: "quay.io/openshift-release-dev/ocp-release:4.17.8-x86_64"
  azure:
    overrides:
      - version: "4.17.9"
        cpoImage: "quay.io/hypershift/control-plane-operator-azure:4.17.9"
      - version: "4.16.20"
        cpoImage: "quay.io/security-fix/cpo-azure:4.16.20-security"
```

## How It Works

### Image Resolution Process

1. **Override Check**: When a HostedCluster is created or updated, the system checks if CPO overrides are enabled
2. **Platform Lookup**: The system looks up overrides for the specific platform (case-insensitive)
3. **Version Match**: If the platform exists, it searches for a version match (see [Version Matching](#version-matching))
4. **Fallback**: If no override is found, the default CPO image from the OpenShift release is used

### Platform Handling

- Platform names are **case-insensitive** (`AWS`, `aws`, `Aws` all work)
- Unknown platforms return empty string (graceful degradation)
- Missing platforms in configuration are handled safely (no panics)

### Version Matching

The version HyperShift compares against is the `metadata.name` of the
`release-manifests/image-references` file inside the release payload image. That
is the payload's own version string, so it includes any suffix the payload was
built with — for example `4.20.15`, `4.20.15-hotfix34123420`, or
`4.20.15-0.nightly-2026-01-01-123456`.

Override lookup runs in two steps:

1. **Exact match**: if an entry's `version` equals the payload version as a
   literal string, that entry applies. `version: "4.20.15"` matches only the
   plain `4.20.15` payload; `version: "4.20.15-hotfix34123420"` matches only
   that build.
2. **Glob match**: otherwise, entries whose `version` contains glob wildcards
   are tried in the order they appear in the file, and the first match applies.
   Patterns use the syntax accepted by Go's
   [`path.Match`](https://pkg.go.dev/path#Match): `*` matches any run of
   characters (except `/`), `?` matches one character, and `[...]` matches a
   character class. A glob must start with a literal `major.minor.patch-`
   prefix, so it can only cover build suffixes of one release and never the
   plain release or another version.

An exact match always wins over a glob, regardless of where the entries are
listed. If several entries repeat the same exact version, the last one wins, so
appending a corrected entry at the end of the file still supersedes the earlier
one. Globs resolve to the first match, so list a more specific pattern before a
less specific one (`4.20.15-hotfix*` before `4.20.15-*`).

Use a glob to cover the suffixed builds of a z-stream without disclosing the
build string, which may be private:

```yaml
platforms:
  aws:
    overrides:
      # Every suffixed build of 4.20.15, e.g. 4.20.15-hotfix34123420.
      - version: "4.20.15-*"
        cpoImage: "quay.io/hypershift/control-plane-operator-aws:4.20.15"
      # Optional: also override the plain 4.20.15 payload.
      - version: "4.20.15"
        cpoImage: "quay.io/hypershift/control-plane-operator-aws:4.20.15"
      # Optional: pin one specific build, overriding the glob above.
      - version: "4.20.15-hotfix34123420"
        cpoImage: "quay.io/hypershift/control-plane-operator-aws:4.20.15-hotfix34123420"
```

With this configuration:

- `4.20.15` uses the second image; the glob does not apply because the pattern
  requires the `-` after the version.
- `4.20.15-hotfix34123420` uses the third image (exact match beats the glob).
- `4.20.15-hotfix99999999` uses the first image.
- `4.21.0` and any other version match nothing and keep the image shipped in
  the payload.

The payload version must be a valid semantic version. HyperShift parses it
while reconciling the HostedCluster and blocks reconciliation when it cannot, so
a suffix such as `-hotfix34123420` or `-0.nightly-2026-01-01-123456` works, but
one with characters semver does not allow (for example `4.20.15-hotfix_1`) does
not make the cluster usable. `oc adm release new --name` enforces the same rule.

Malformed or unscoped patterns are rejected when the overrides are loaded (the
operator fails to start with an `invalid version pattern` error), so a typo such
as `4.20.15-[` cannot silently disable an override, and a pattern such as
`4.20.*` or `4.20.15*` cannot widen the match by accident.

## Enabling CPO Overrides

CPO overrides are controlled by an environment variable on the HyperShift operator:

```bash
export ENABLE_CPO_OVERRIDES=1
```

When this environment variable is set to `1`, the override system is activated. Without this variable, all overrides are ignored and default images are used.

## File Locations

The override configuration files are embedded in the binary at build time:

`hypershift-operator/controlplaneoperator-overrides/assets/overrides.yaml`


## Best Practices

### Version Management
- Use exact `major.minor.patch` strings (e.g., "4.17.9", not "4.17.x") for the plain payload of a release
- Use a glob such as "4.17.9-*" to cover suffixed builds (hotfix, nightly) of that release without listing the build string
- Add a full suffixed version string (e.g., "4.17.9-hotfix34123420") only when one specific build must be pinned over the glob
- Keep override lists sorted by version for maintainability

### Image References
- Use full image references including registry and tag/digest
- Prefer digest references for production overrides for immutability

### Platform Configuration
- Only configure platforms that need overrides
- Keep platform-specific testing configurations separate
- Document the purpose of each override in comments

### Testing
- The test section allows specifying a pair of release images to use for a single hypershift e2e test run and controlling which platform's override tests should run
- The `runTests` field controls whether override tests run for each platform when `TEST_CPO_OVERRIDE=1` is set
- **CI Resource Optimization**: Typically, only one platform should have `runTests: true` at a time to avoid wasting CI resources
  - If a PR only updates AWS overrides, set `runTests: true` for AWS and `runTests: false` for Azure
  - If a PR only updates Azure overrides, set `runTests: true` for Azure and `runTests: false` for AWS
- **Testing Multiple Platforms**: Both platforms can have `runTests: true` simultaneously if your changes affect both platforms (e.g., structural changes to the override system itself)

#### Running Override Tests

The `TEST_CPO_OVERRIDE` environment variable enables override testing across the e2e test suite.

**What TEST_CPO_OVERRIDE does:**
1. Configures all e2e tests to use the release images specified in the `testing` section of `overrides.yaml` instead of the default latest/previous releases
2. In CI environments, automatically enables CPO overrides in the HyperShift operator (`--enable-cpo-overrides` flag)
3. Triggers verification in `TestCreateCluster` that validates the CPO pod is running with the expected override image

**How override verification works:**
When `TEST_CPO_OVERRIDE=1` is set, the `TestCreateCluster` test automatically verifies that the control-plane-operator pod is running with the correct override image from `overrides.yaml`. This verification happens for all platforms during the standard cluster creation test, so no dedicated override test is needed.

To run override tests:

1. Set the `TEST_CPO_OVERRIDE` environment variable:
   ```bash
   export TEST_CPO_OVERRIDE=1
   ```

2. Configure the testing release images in `overrides.yaml` and set which platform(s) to test:
   ```yaml
   platforms:
     aws:
       testing:
         latest: "quay.io/openshift-release-dev/ocp-release:4.19.10-x86_64"
         previous: "quay.io/openshift-release-dev/ocp-release:4.19.9-x86_64"
         runTests: true  # Set to true to test this platform's overrides
     azure:
       testing:
         latest: "quay.io/openshift-release-dev/ocp-release:4.19.10-x86_64"
         previous: "quay.io/openshift-release-dev/ocp-release:4.19.9-x86_64"
         runTests: false  # Set to false to skip this platform's tests (saves CI resources)
   ```

   Note: Both platforms can have `runTests: true` if your changes affect both platforms.

3. When running locally, ensure CPO overrides are enabled when installing the operator:
   ```bash
   bin/hypershift install --enable-cpo-overrides ...
   ```

4. Run the e2e tests on the desired platform:
   - All tests will use the configured override test releases
   - `TestCreateCluster` will automatically verify the CPO override image is correctly applied

## Testing With a Payload That Carries a Version Suffix

To validate suffix matching end to end you do not need a real hotfix payload:
you can publish a copy of any regular release payload under a version that has
an arbitrary suffix. `oc adm release new --name` rewrites the payload's
`image-references` name — the value HyperShift reads as the release version —
while keeping every component image and manifest of the source payload.

The example below uses AWS and `4.20.15` with the suffix `-hotfix34123420`.

### 1. Publish the payload copy

```bash
# Registry credentials with access to the source payload's component images.
PULL_SECRET=${PULL_SECRET:-/path/to/pull-secret.json}

SOURCE=quay.io/openshift-release-dev/ocp-release:4.20.15-x86_64
# Any registry/tag you can push to. Keep the tag version-shaped (see the note below).
TARGET=quay.io/<your-org>/ocp-release:4.20.15-hotfix34123420

oc adm release new \
  --registry-config "${PULL_SECRET}" \
  --from-release "${SOURCE}" \
  --name 4.20.15-hotfix34123420 \
  --to-image "${TARGET}"
```

`--name` must be a valid semantic version, so use a semver-valid suffix such as
`-hotfix34123420`; `oc adm release new` rejects names it cannot parse.
Rebuilding pulls the manifests of every operator image in the source payload but
mirrors no component images — the copy still references the originals — so it
takes a few minutes and no large pushes.

Confirm the new payload reports the suffixed version:

```bash
oc adm release info "${TARGET}" | head -3
# Name:           4.20.15-hotfix34123420
```

You can also inspect the exact value HyperShift reads:

```bash
oc adm release extract --from="${TARGET}" --to=/tmp/suffixed-payload
python3 -c 'import json; print(json.load(open("/tmp/suffixed-payload/image-references"))["metadata"]["name"])'
# 4.20.15-hotfix34123420
```

To check the payload without pushing it anywhere, replace `--to-image` with
`--to-dir /tmp/suffixed-payload`; the resulting `image-references` has the same
`metadata.name`.

!!! note
    Override matching only cares about `--name`, not the image tag. Keep the tag
    version-shaped anyway (`:4.20.15-hotfix34123420`): the `TEST_CPO_OVERRIDE`
    e2e helpers derive the expected version from the image tag, not from the
    payload contents.

### 2. Add the override and enable overrides

Add a glob override so it covers the suffixed build without naming it:

```yaml
platforms:
  aws:
    overrides:
      - version: "4.20.15-*"
        cpoImage: "quay.io/hypershift/control-plane-operator-aws:4.20.15"
```

Add a plain `version: "4.20.15"` entry as well if the un-suffixed payload should
be overridden too; the glob alone does not match it.

Rebuild the HyperShift operator image with the updated `overrides.yaml` and
install or upgrade it with overrides enabled:

```bash
bin/hypershift install --enable-cpo-overrides ...
```

That flag sets `ENABLE_CPO_OVERRIDES=1` on the operator deployment. On an
existing installation you can instead patch the deployment:

```bash
oc -n hypershift set env deployment/operator ENABLE_CPO_OVERRIDES=1
```

### 3. Create or point a HostedCluster at the suffixed payload

For a fresh cluster, pass the rebuilt payload as the release image:

```bash
hypershift create cluster aws \
  --name suffix-test \
  --release-image "${TARGET}" \
  ...
```

For an existing cluster, set it as the control plane release so only the
control plane (where the override is applied) moves to the new payload:

```bash
oc patch hostedcluster -n clusters <cluster-name> --type merge \
  -p "{\"spec\":{\"controlPlaneRelease\":{\"image\":\"${TARGET}\"}}}"
```

### 4. Verify the override was applied

The control-plane-operator pod must run the override image, not the `hypershift`
image from the payload:

```bash
oc get pods -n clusters-<cluster-name> -l app=control-plane-operator \
  -o jsonpath='{.items[*].spec.containers[?(@.name=="control-plane-operator")].image}'
# quay.io/hypershift/control-plane-operator-aws:4.20.15
```

Useful negative and precedence checks:

- With only `version: "4.20.15-*"` configured, a HostedCluster pointed at
  `${TARGET}` must show the override image, while one pointed at
  `ocp-release:4.20.15-x86_64` must keep the `hypershift` image shipped in the
  payload: the glob requires the `-` after the version.
- Add `version: "4.20.15"` as well: the plain payload now uses the override too,
  and `${TARGET}` still matches the glob.
- Add `version: "4.20.15-hotfix34123420"`: `${TARGET}` uses that entry's image
  (exact match wins over the glob), while a payload named
  `4.20.15-hotfix99999999` keeps the glob image.
- Point a HostedCluster at a payload with a different base version (e.g.
  `4.21.0`): it must keep the payload's `hypershift` image.

### 5. Run the unit tests

```bash
go test ./hypershift-operator/controlplaneoperator-overrides/... \
  -run 'TestCPOImage|TestBaseVersion' -v
go test ./support/imageregistry/... -run TestGetControlPlaneOperatorImage -v
```

The `support/imageregistry` package uses generated mocks; run `make generate`
first if the package fails to build with missing `NewMock*` symbols.

## Troubleshooting

### Common Issues

1. **Override Not Applied**
   - Check if `ENABLE_CPO_OVERRIDES=1` is set
   - Verify platform name matches exactly (case-insensitive)
   - Check which version HyperShift sees for the payload:
     `oc adm release info <payload-image> | head -3`. If it carries a suffix
     (e.g., `4.20.15-hotfix34123420`), add a matching glob such as
     `version: "4.20.15-*"` or the exact suffixed string; a plain `4.20.15`
     entry does not match suffixed payloads. See
     [Version Matching](#version-matching)
   - Among globs, the first matching entry in the file wins; exact entries
     always win over globs
   - A malformed pattern, or one not scoped to a `major.minor.patch-` prefix
     (e.g. `4.20.*`), makes the operator fail at startup with an
     `invalid version pattern` error instead of silently ignoring the override

2. **Image Pull Failures**
   - Verify image exists and is accessible
   - Check image registry authentication
   - Validate image digest/tag is correct

3. **Platform Not Found**
   - Returns empty string (safe fallback)
   - Check platform configuration in YAML
   - Verify platform name spelling


## Creating Override PRs with Claude Code

The `/create-cpo-override` Claude Code skill automates the entire process of
creating a CPO override PR:

1. **Gathers inputs** — platform(s), branches, version ranges, required fix
   PRs, and optionally specific images
2. **Detects pull secret** — probes whether the container runtime can access
   authenticated registries; falls back to `$PULL_SECRET` or asks for a path
3. **Auto-resolves images** — for each branch, tries (in order): the latest
   OCP stable payload, the latest fast payload, and Konflux push builds.
   Stops at the first image that contains all required fix PRs
4. **Verifies fixes** — runs `verify-pr-in-image.sh` (same script used by
   the validation skill) against every (branch, image, PR) tuple
5. **Tests pullability** — confirms each image is accessible via `skopeo`
6. **Presents a summary** — shows branches, version ranges, images, sources,
   and verification results for user confirmation
7. **Edits `overrides.yaml`** — adds entries matching the existing file
   conventions
8. **Prepares the commit and PR** — generates the validation contract lines
   that `/validate-pr-override-images` expects, then runs the validator
   against the PR after creation

```
# In Claude Code:
/create-cpo-override
```

The skill queries the Cincinnati API to resolve version ranges (including
z-streams that Cincinnati skips but clusters can still run) and uses
`oc adm release info` to extract the `hypershift` image from OCP payloads.

### Image source priority

| Priority | Source | Registry pattern |
|----------|--------|-----------------|
| 1 | Latest stable payload | `quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:...` |
| 2 | Latest fast payload | same |
| 3 | Konflux push build | `quay.io/redhat-user-workloads/crt-redhat-acm-tenant/control-plane-operator-X-Y@sha256:...` |
| 4 | User-provided image | any |

If no automatically discovered image contains the required fixes, the skill
points to the `build-cpo-image` dev skill and the Konflux hotfix process
documented in `contrib/konflux/README.md`.

## Validating Override Images Contain Claimed PRs

When reviewing a PR that sets CPO override images and claims "includes
fix from PR #8500 and PR #8512", you can verify the claimed PRs are
actually present in the image's git history using the
`/validate-pr-override-images` Claude Code skill.

The PR description must include a structured contract:

```
branch: 4.20 wants: https://github.com/openshift/hypershift/pull/8593
branch: 4.21 wants: https://github.com/openshift/hypershift/pull/8593, https://github.com/openshift/hypershift/pull/8565
```

```
# In Claude Code, pass the PR number or URL:
/validate-pr-override-images 8610
/validate-pr-override-images https://github.com/openshift/hypershift/pull/8610
```

The skill reads the `vcs-ref` label from each override image via
`skopeo inspect` to get the commit SHA the image was built from, then
uses `git merge-base --is-ancestor` to check whether each claimed PR's
merge commit is an ancestor of that image commit.

Prerequisites: `skopeo`, `gh`, relevant release branches fetched locally

!!! note
    The `validate-cpo-overrides.yaml`
    [GitHub Action](../how-to/ci/github-actions.md) runs this same
    validation automatically on PRs that touch `overrides.yaml`.

## Security/Production Considerations

- Only use trusted image registries for CPO overrides
- Validate override images are signed and verified
- If multiple architectures are supported in the data plane, ensure that override image references point to multi-arch repositories that have all architectures supported in the data plane.