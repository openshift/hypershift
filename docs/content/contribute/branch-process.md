## OCP Branching Tasks for the HyperShift Team
These are a set of tasks we need to perform on every OCP branching. We need to:

1. Update the HyperShift Repository to add the latest supported OCP version - [Update Supported Version](#update-supported-version)
1. Update the base images in our Dockerfiles (if they are available at branching) - [Update Dockerfiles](#update-dockerfiles)
1. Update the Renovate configuration to include the new release branch - [Update Renovate](#update-renovate-configuration)
1. Update the OpenShift Release repository to fix the step registry configuration files - [OpenShift/Release](#openshiftrelease-repository)
1. Update TestGrid to include the new OCP version tests - [TestGrid](#update-testgrid)
1. Add upgrade-from-.0 periodic jobs for ROSA and ARO HCP once the new version is GA - [Upgrade-from-.0 Periodics](#add-upgrade-from-0-periodic-jobs)

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

We should also ensure that the latest release branch is using the Hypershift Operator and e2e from main. Specifically, the release branch CI config should import `hypershift-tests` as a pre-built base image from the `hypershift` namespace rather than building it from `Dockerfile.e2e` in the release branch source. This ensures the e2e binary comes from main and keeps release branch configs consistent. The changes needed are:

1. Add `hypershift-tests` to the `base_images` section (namespace: `hypershift`, tag: `latest`)
2. Remove the `Dockerfile.e2e` entry from the `images` section
3. Remove `hypershift-tests` from the promotion exclusion list (since it is no longer built)

[Example Release Branch PR (4.21)](https://github.com/openshift/release/pull/69341/files)

[Example Release Branch PR (4.22)](https://github.com/openshift/release/pull/78912/files)

---

### Update TestGrid
We need to update TestGrid to include the new OCP version tests. 

Here is an [Example PR](https://github.com/kubernetes/test-infra/pull/35535) to do that.

---

### Add Upgrade-from-.0 Periodic Jobs

Once a new OCP minor version goes GA, we need to add upgrade-from-.0 periodic jobs for both AWS (ROSA) and Azure (ARO HCP) platforms in the [openshift/release](https://github.com/openshift/release) repository. These jobs validate that clusters can upgrade from CPO version 4.Y.0 to 4.Y.latest without triggering NodePool rollouts.

Jobs to create for each new GA version:

- **AWS (ROSA):** `periodic-ci-openshift-hypershift-release-4.Y-periodics-hcm-upgrade-dot-zero-to-latest-aws-ovn`
- **Azure (ARO HCP):** `periodic-ci-openshift-hypershift-release-4.Y-periodics-hcm-upgrade-dot-zero-to-latest-azure`

The CI operator configs live at:

- `ci-operator/config/openshift/hypershift/openshift-hypershift-release-4.Y__periodics-hcm-upgrade.yaml` (AWS)
- `ci-operator/config/openshift/hypershift/openshift-hypershift-release-4.Y__periodics-hcm-azure.yaml` (Azure)

Use the existing job configurations from the prior OCP version as a template. Both jobs run the `TestUpgradeControlPlane` test with `PREVIOUS_RELEASE_IMAGE` set to 4.Y.0 and `LATEST_RELEASE_IMAGE` set to 4.Y.latest.

For detailed requirements and job specifications, see:

- [CNTRLPLANE-1852](https://redhat.atlassian.net/browse/CNTRLPLANE-1852) — AWS (ROSA) upgrade-from-.0 periodic job spec
- [CNTRLPLANE-1854](https://redhat.atlassian.net/browse/CNTRLPLANE-1854) — Azure (ARO HCP) upgrade-from-.0 periodic job spec
