---
title: Release process
---

# Release process

!!! important

    This is a complex process that involves some changes in multiple repositories and will affect multiple teams daily basis work.
    Make sure you have multiple reviewers from Core dev team which could guide you in the full process.

## Preparing a release in [Openshift/Hypershift](https://github.com/openshift/hypershift) repository

### Bumping release version and generating Release Notes

The [hypershift repo]( https://github.com/openshift/hypershift) produces two different artifacts: Hypershift Operator (HO) and Control Plane Operator (CPO).

The CPO release lifecycle is dictated by the [OCP release payload](https://access.redhat.com/support/policy/updates/openshift).

The HO has an independent release cadence. For consumer products:

- Our internal image build system builds from our latest commit in main several times a day.
- To roll out a new build we apply the following process:
  - Create a git tag for the commit belonging to the image to be rolled out:
    - `git co $commit-sha`
    - `git tag v0.1.1`
    - Push against remote.
  - Generate release notes:
    - `GITHUB_ACCESS_TOKEN=XXXX FROM=v0.1.0 TO=v0.1.1 make release`
    - Use the output to create the PR for bump the new image in the product gitOps repo. E.g.

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

### Supported Versions

This change involves basically the OCP supported versions by Hypershift operator:

- `support/supportedversion/version.go` which contains the variable called `LatestSupportedVersion`. This one contains, as you can imagine, the Latest supported version. We need to put the new version here.
- `support/supportedversion/version_test.go` contains the tests for the to validate the Latest version. It should comply with the established contract to support 2 versions prior to the Latest.

We need also to modify some things manually in the Openshift/Release repo.

## Preparing a release in [Openshift/Release](https://github.com/openshift/release) repository

### Step Registry/CLI

From folder: `ci-operator/step-registry/hypershift/` in the OCP release repo. In each file we will have a section called `from_image:` overriding the current release version (E.G) `"4.11"` to the new one `"4.12"`. You can check an example [here](https://github.com/openshift/release/pull/30297/files):

- `aws/create/hypershift-aws-create-chain.yaml`
- `aws/destroy-nested-management-cluster/hypershift-aws-destroy-nested-management-cluster-chain.yaml`
- `aws/destroy/hypershift-aws-destroy-chain.yaml`
- `aws/setup-nested-management-cluster/hypershift-aws-setup-nested-management-cluster-chain.yaml`
- `aws/run-e2e/external/hypershift-aws-run-e2e-external-chain.yaml`
- `azure/create/hypershift-azure-create-chain.yaml`
- `azure/destroy/hypershift-azure-destroy-chain.yaml`
- `dump/hypershift-dump-chain.yaml`
- `install/hypershift-install-chain.yaml`
- `kubevirt/create/hypershift-kubevirt-create-chain.yaml`

### Mirroring Images for CPO and HO

For folder `core-services/image-mirroring/hypershift/` we need to add a mapping file for each version we want to mirror. This is a sample of how it looks like this kind of [file](https://github.com/openshift/release/blob/master/core-services/image-mirroring/hypershift/mapping_hypershift_4_13). This sample points to version `4.13` and the filename is `mapping_hypershift_4_13`:

```
registry.ci.openshift.org/ocp/4.13:hypershift quay.io/hypershift/hypershift:4.13
registry.ci.openshift.org/ocp/4.13:hypershift-operator quay.io/hypershift/hypershift-operator:4.13
registry.ci.openshift.org/ocp-arm64/4.13:hypershift quay.io/hypershift/hypershift:4.13-arm64 quay.io/hypershift-arm64/hypershift:latest-arm64
registry.ci.openshift.org/ocp-arm64/4.13:hypershift-operator quay.io/hypershift/hypershift-operator:4.13-arm64
```

As you can see, the version is hardcoded but, what happens to the `latest` version?:

```
registry.ci.openshift.org/ocp/4.14:hypershift quay.io/hypershift/hypershift:4.14 quay.io/hypershift/hypershift:latest
registry.ci.openshift.org/ocp/4.14:hypershift-operator quay.io/hypershift/hypershift-operator:4.14  quay.io/hypershift/hypershift-operator:latest
registry.ci.openshift.org/ocp-arm64/4.14:hypershift quay.io/hypershift/hypershift:4.14-arm64 quay.io/hypershift/hypershift:latest-arm64
registry.ci.openshift.org/ocp-arm64/4.14:hypershift-operator quay.io/hypershift/hypershift-operator:4.14-arm64  quay.io/hypershift/hypershift-operator:latest-arm64
```

We should create another file which contains the pointer to the `latest` version.

### Conformance Jobs

From folder `ci-operator/config/openshift/hypershift` we need to focus in the `releases` section. You need to modify the files in this manner (maybe more than one time in each file), (E.G) from `"4.11"` bumping to `"4.12"`, in the nexts files:

- `openshift-hypershift-main.yaml`
- `openshift-hypershift-main__periodics.yaml`
- `openshift-hypershift-release-4.11__periodics.yaml`
- `openshift-hypershift-main-periodics.yaml`
- `openshift-hypershift-main-presubmits.yaml`
- `openshift-hypershift-release-4.11-periodics.yaml`

**Add new Presubmits jobs**:

- `openshift-hypershift-release-4.11-presubmits.yaml`

**Delete the older Presubmits jobs with unsupported versions**:

- `openshift-hypershift-release-4.10-presubmits.yaml`
## References

- [PR With a Release Upgrade](https://github.com/openshift/release/pull/30297/files)
- [PR Mirroring images for CPO and HO](https://github.com/openshift/release/pull/30215)
- [PR Updating Conformace Jobs](https://github.com/openshift/release/pull/29972)
- [PR with Tools and Latest supported version](https://github.com/openshift/hypershift/pull/1575/files#diff-63fe5b9f7f4d0e344d2dfbf49aac7b3b9b46299371061221e5ff55d4a58f7db9R13)