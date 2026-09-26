---
title: Release process
---

# HO/CPO Release process

!!! important

    This is a complex process that involves some changes in multiple repositories and will affect multiple teams daily basis work.
    Make sure you have multiple reviewers from Core dev team which could guide you in the full process.

## Preparing a release in [Openshift/Hypershift](https://github.com/openshift/hypershift) repository

### Bumping release version and generating Release Notes

The [hypershift repo](https://github.com/openshift/hypershift) produces two different artifacts: Hypershift Operator (HO) and Control Plane Operator (CPO).

The CPO release lifecycle is dictated by the [OCP release payload](https://access.redhat.com/support/policy/updates/openshift).

The HO has an independent release cadence. For consumer products:

- Our internal image build system builds from our latest commit in main several times a day.
- To roll out a new build we apply the following process:

#### Automated flow (recommended)

The release process is managed via GitHub Actions workflows triggered by changes to `releases/tags.yaml`.

1. **Request a tag**: Open a PR adding an entry to [`releases/tags.yaml`](https://github.com/openshift/hypershift/blob/main/releases/tags.yaml):

    ```yaml
    tags:
      - name: "v0.1.47"
        commit: "abc123def456789..."  # Full 40-char SHA from main
        description: "HO release for ROSA 4.17.8 rollout"
    ```

2. **Validation**: The `validate-tag-request` workflow automatically validates:
    - Tag name is valid semver (`v<major>.<minor>.<patch>`)
    - Commit SHA exists and is reachable from `main`
    - Tag does not already exist
    - No duplicate tag names in the manifest

3. **Tag creation**: Once the PR is reviewed and merged, the `create-tag` workflow creates an annotated git tag at the specified commit and pushes it.

4. **Draft release**: The tag push triggers `create-release`, which:
    - Generates release notes using `hack/tools/release/notes.go`
    - Builds a source tarball with SHA256 checksum
    - Creates a **draft** GitHub Release

5. **Attach CLI binaries**: After Konflux builds the CLI image, attach the binaries to the draft release and promote it:

    ```bash
    gh workflow run attach-release-artifacts.yaml \
      -f tag=v0.1.47 \
      -f cli_image=quay.io/redhat-user-workloads/...
    ```

    This extracts the CLI binary from the container image, uploads it to the GitHub Release, and promotes the release from draft to published.

    !!! note

        Automating the `attach-release-artifacts` trigger from Konflux pipelines is tracked in [CNTRLPLANE-4380](https://issues.redhat.com/browse/CNTRLPLANE-4380).

#### Legacy manual flow

For cases where the automated flow is not available:

  - Create a git tag for the commit belonging to the image to be rolled out:
    - `git co $commit-sha`
    - `git tag v0.1.1`
    - Push against remote.
  - Generate release notes:
    - `FROM=v0.1.0 TO=v0.1.1 make release`
    - Use the output to create the PR for bump the new image in the product gitOps repo. E.g.

### Release notes sample

This is a sample of how the release notes looks like added to the PR:

  ```
  ## area/control-plane-operator

  - [cpo: cno: follow image name change in release payload](https://github.com/openshift/hypershift/pull/2230)

  ## area/hypershift-operator

  - [Added documentation around supported-versions configmap](https://github.com/openshift/hypershift/pull/2220)
  - [Add comment for BaseDomainPrefix](https://github.com/openshift/hypershift/pull/2219)
  - [Add condition to NodePool indicating whether a security group for it is available](https://github.com/openshift/hypershift/pull/2216)
  - [HOSTEDCP-827: Add root volume encryption e2e test](https://github.com/openshift/hypershift/pull/2192)
  - [fix(hypershift): reduce CAPI rbac access](https://github.com/openshift/hypershift/pull/2173)
  - [Validate Network Input for HostedCluster](https://github.com/openshift/hypershift/pull/2215)
  ```
