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
    - `FROM=v0.1.0 TO=v0.1.1 make release`
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

## OCP Release Tasks

### HyperShift Repository

We need to add the latest supported version in the `hypershift` repository. We need to modify two files:

- `support/supportedversion/version.go` which contains the variable called `LatestSupportedVersion`. This one contains, as you can imagine, the Latest supported version. We need to put the new version here.
- `support/supportedversion/version_test.go` contains the tests for the to validate the Latest version. It should comply with the established contract to support 2 versions prior to the Latest.

[Example Supported Version Bump PR](https://github.com/openshift/hypershift/pull/5146/files)

We also need to bump the base images in our Dockerfiles.

[Example Base Image Bump PR](https://github.com/openshift/hypershift/pull/5195/files)

### [Openshift/Release](https://github.com/openshift/release) Repository

The Step registry config should be updated by Test Platform. However, the Test Platform is not aware of custom configurations of the different version for specific hypershift tests.
So, we need to check over the Step registry config and make sure that the hypershift tests are correctly configured. Below is an example of the necessary changes to the Step registry config after test platform bumps:

[Example Release Repo PR](https://github.com/openshift/release/pull/59120/files)

We should also ensure that the latest release branch is using the Hypershift Operator and e2e from main.

[Example Release Branch PR](https://github.com/openshift/release/pull/60531/files)

### Notes
If test platform are testing new OCP releases before the release is cut the hypershift test will fail and block payloads until:
- There are at least two accepted nightly payloads for the new release.
- The supported versions in the HyperShift repository are updated.
