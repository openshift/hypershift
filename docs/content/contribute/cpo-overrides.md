# CPO Overrides Configuration Guide

## Overview

The CPO (Control Plane Operator) overrides mechanism allows you to specify custom Control Plane Operator images for specific OpenShift versions and platforms. This is particularly useful for applying hotfixes and patches before they're officially released.

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

- **`version`** (string): The exact OpenShift version (e.g., "4.17.9", "4.16.17")
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
3. **Version Match**: If the platform exists, it searches for an exact version match
4. **Fallback**: If no override is found, the default CPO image from the OpenShift release is used

### Platform Handling

- Platform names are **case-insensitive** (`AWS`, `aws`, `Aws` all work)
- Unknown platforms return empty string (graceful degradation)
- Missing platforms in configuration are handled safely (no panics)

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
- Use exact version strings (e.g., "4.17.9", not "4.17.x")
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

## Troubleshooting

### Common Issues

1. **Override Not Applied**
   - Check if `ENABLE_CPO_OVERRIDES=1` is set
   - Verify platform name matches exactly (case-insensitive)
   - Confirm version string is exact match

2. **Image Pull Failures**
   - Verify image exists and is accessible
   - Check image registry authentication
   - Validate image digest/tag is correct

3. **Platform Not Found**
   - Returns empty string (safe fallback)
   - Check platform configuration in YAML
   - Verify platform name spelling


## Security/Production Considerations

- Only use trusted image registries for CPO overrides
- Validate override images are signed and verified
- If multiple architectures are supported in the data plane, ensure that override image references point to multi-arch repositories that have all architectures supported in the data plane.