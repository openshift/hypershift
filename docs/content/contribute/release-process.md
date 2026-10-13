---
title: Release process
---

# HO/CPO Release process

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
