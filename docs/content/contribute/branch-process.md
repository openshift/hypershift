## OCP Branching Tasks for the HyperShift Team
These are a set of tasks we need to perform on every OCP branching. We need to:

1. Update the HyperShift Repository to add the latest supported OCP version - [Update Supported Version](#update-supported-version)
1. Update the base images in our Dockerfiles (if they are available at branching) - [Update Dockerfiles](#update-dockerfiles)
1. Update the Renovate configuration to include the new release branch - [Update Renovate](#update-renovate-configuration)
1. Update the OpenShift Release repository to fix the step registry configuration files - [OpenShift/Release](#openshiftrelease-repository)
1. Update TestGrid to include the new OCP version tests - [TestGrid](#update-testgrid)

!!! danger
    If test platform are testing new OCP releases before the release is cut the hypershift test will fail and block payloads until:

    - There are at least two accepted nightly payloads for the new release.
    - The supported versions in the HyperShift repository are updated.

---
### HyperShift Repository

#### Update Supported Version
We need to add the latest supported version in the `hypershift` repository. We need to modify two files:

- `support/supportedversion/version.go` which contains the variable called `LatestSupportedVersion`. This one contains, as you can imagine, the Latest supported version. We need to put the new version here.
- `support/supportedversion/version_test.go` contains the tests to validate the Latest version. It should comply with the established contract to support 2 versions prior to the Latest.

[Example Supported Version Bump PR](https://github.com/openshift/hypershift/pull/5146/files)

#### Update Dockerfiles
We also need to bump the base images in our Dockerfiles.

[Example Base Image Bump PR](https://github.com/openshift/hypershift/pull/5195/files)

#### Update Renovate Configuration
We need to add the new release branch to the Renovate configuration so that security updates are automatically applied to the release branch.

Update `renovate.json` to include the new release branch in two places:

1. Add the new branch to the `baseBranches` array at the top of the file
2. Add the new branch to the `matchBaseBranches` array in the security-only Go updates package rule

!!! note
    If any release branch has reached End of Life and is no longer supported, remove it from both locations in `renovate.json` to stop automated updates on that branch.

Example change for release-4.21:
```json
{
  "baseBranches": [
    "main",
    "release-4.16",
    "release-4.17",
    "release-4.18",
    "release-4.19",
    "release-4.20",
    "release-4.21"
  ],
  "packageRules": [
    {
      "description": "Enable security-only Go updates on release branches",
      "matchManagers": ["gomod"],
      "matchBaseBranches": [
        "release-4.16",
        "release-4.17",
        "release-4.18",
        "release-4.19",
        "release-4.20",
        "release-4.21"
      ],
      "matchUpdateTypes": ["patch"]
    }
  ]
}
```

---

### [Openshift/Release](https://github.com/openshift/release) Repository
The Step registry config should be updated by Test Platform. However, the Test Platform is not aware of custom configurations of the different version for specific hypershift tests.
So, we need to check over the Step registry config and make sure that the hypershift tests are correctly configured. Below is an example of the necessary changes to the Step registry config after test platform bumps:

[Example Release Repo PR](https://github.com/openshift/release/pull/59120/files)

We should also ensure that the latest release branch is using the Hypershift Operator and e2e from main.

[Example Release Branch PR](https://github.com/openshift/release/pull/69341/files)

---

### Update TestGrid
We need to update TestGrid to include the new OCP version tests. 

Here is an [Example PR](https://github.com/kubernetes/test-infra/pull/35535) to do that.
