//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EnvVarSpec describes an environment variable used by the test suite
type EnvVarSpec struct {
	Name        string
	Description string
	Required    bool
	Default     string
}

var (
	// envVarRegistry tracks all environment variables used by the test suite
	envVarRegistry = make(map[string]EnvVarSpec)
)

// RegisterEnvVar registers an environment variable specification.
// This should be called in init() functions to document environment variables used by the test suite.
// Example:
//
//	RegisterEnvVar("MY_VAR", "Description of what this variable does", true)
func RegisterEnvVar(name, description string, required bool) {
	envVarRegistry[name] = EnvVarSpec{
		Name:        name,
		Description: description,
		Required:    required,
	}
}

// RegisterEnvVarWithDefault registers an environment variable specification with a default value.
// This should be called in init() functions to document environment variables used by the test suite.
// Example:
//
//	RegisterEnvVarWithDefault("MY_VAR", "Description of what this variable does", false, "default-value")
func RegisterEnvVarWithDefault(name, description string, required bool, defaultValue string) {
	envVarRegistry[name] = EnvVarSpec{
		Name:        name,
		Description: description,
		Required:    required,
		Default:     defaultValue,
	}
}

// GetEnvVarValue returns the value of an environment variable, or its default if not set.
// Panics if the environment variable is not registered in the registry.
func GetEnvVarValue(name string) string {
	spec, exists := envVarRegistry[name]
	if !exists {
		panic(fmt.Sprintf("environment variable %q is not registered. Use RegisterEnvVar or RegisterEnvVarWithDefault to register it", name))
	}

	value := os.Getenv(name)
	if value == "" && spec.Default != "" {
		return spec.Default
	}
	return value
}

// PrintEnvVarHelp prints a formatted help message for all registered environment variables.
// Output goes to stderr to avoid interfering with test framework stdout.
func PrintEnvVarHelp() {
	w := os.Stderr
	if len(envVarRegistry) == 0 {
		fmt.Fprintln(w, "No environment variables are registered.")
		return
	}

	fmt.Fprintln(w, "Environment Variables:")
	fmt.Fprintln(w, strings.Repeat("=", 80))

	var names []string
	for name := range envVarRegistry {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		spec := envVarRegistry[name]
		fmt.Fprintf(w, "\n%s", name)
		if spec.Required {
			fmt.Fprint(w, " (required)")
		} else {
			fmt.Fprint(w, " (optional)")
		}
		if spec.Default != "" {
			fmt.Fprintf(w, " [default: %s]", spec.Default)
		}
		fmt.Fprintf(w, "\n  %s\n", spec.Description)
		if currentValue := os.Getenv(name); currentValue != "" {
			fmt.Fprintf(w, "  Current value: %s\n", maskSensitiveValue(name, currentValue))
		}
	}
	fmt.Fprintln(w)
}

// maskSensitiveValue masks potentially sensitive environment variable values
func maskSensitiveValue(name, value string) string {
	lowerName := strings.ToLower(name)
	if strings.Contains(lowerName, "secret") ||
		strings.Contains(lowerName, "password") ||
		strings.Contains(lowerName, "token") ||
		strings.Contains(lowerName, "key") {
		if len(value) > 8 {
			return value[:4] + "..." + value[len(value)-4:]
		}
		return "****"
	}
	return value
}

func init() {
	RegisterEnvVar("SSH_SOURCE_CIDR", "Optional CIDR permitted for AWS worker journal SSH diagnostics during destroy; the diagnostic consumer uses 0.0.0.0/0 when empty.", false)
	RegisterEnvVar("E2E_EXPECTED_NODE_POOLS", "JSON map of initial NodePool names to replicas from rendered creation intent; optional for standalone suites.", false)
	RegisterEnvVar("E2E_HEALTH_WORKER_PHASE", "Worker applicability for health assertions: after discovers current workers with a ten-minute budget; empty preserves initial intent or standalone discovery.", false)
	// Register environment variables used by the test suite
	RegisterEnvVar(
		"E2E_HOSTED_CLUSTER_NAME",
		"Name of the HostedCluster to test. Required for tests that interact with a hosted cluster.",
		false,
	)
	RegisterEnvVar(
		"E2E_HOSTED_CLUSTER_NAMESPACE",
		"Namespace of the HostedCluster to test. Required for tests that interact with a hosted cluster.",
		false,
	)
	RegisterEnvVar(
		"E2E_SHOW_ENV_HELP",
		"When set to any non-empty value, displays environment variable help and exits without running tests.",
		false,
	)
	RegisterEnvVarWithDefault(
		"ARTIFACT_DIR",
		"Directory for test artifacts. Defaults to /tmp/artifacts.",
		false,
		"/tmp/artifacts",
	)
	RegisterEnvVarWithDefault(
		"AWS_GUEST_INFRA_CREDENTIALS_FILE",
		"Path to the AWS guest infrastructure credentials file. Defaults to ~/.aws/credentials.",
		false,
		filepath.Join(os.Getenv("HOME"), ".aws", "credentials"),
	)
	RegisterEnvVarWithDefault(
		"E2E_SERVICE_DOMAIN",
		"Service domain used for custom DNS endpoint testing. Optional; test is skipped when empty.",
		false,
		"",
	)
	// Azure self-managed test environment variables
	RegisterEnvVar(
		"AZURE_PRIVATE_NAT_SUBNET_ID",
		"Azure resource ID of the NAT subnet for Private Link Service. Auto-created by PLS controller if not set.",
		false,
	)
	RegisterEnvVar(
		"AZURE_PRIVATE_ADDITIONAL_ALLOWED_SUBSCRIPTIONS",
		"Comma-separated list of Azure subscription IDs permitted to create Private Endpoints.",
		false,
	)
	// Release image env vars for lifecycle tests
	RegisterEnvVar(
		"E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET",
		"Optional CP upgrade target for asserting health and failure diagnostics; must equal E2E_LATEST_RELEASE_IMAGE. The AWS runner sets this from the first child of ordinary CP and AutoNode upgrade fixtures, independently of release-version gates.",
		false,
	)
	RegisterEnvVar(
		"E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE",
		"Post-control-plane-upgrade release selector for asserting health and failure diagnostics; must equal E2E_LATEST_RELEASE_IMAGE. The runner sets this per child after a verified successful ordinary CP spec; absent preserves initial previous-with-latest-fallback gates.",
		false,
	)
	RegisterEnvVar(
		"E2E_LATEST_RELEASE_IMAGE",
		"Latest OCP release image for control plane upgrade tests.",
		false,
	)
	RegisterEnvVar(
		"E2E_PREVIOUS_RELEASE_IMAGE",
		"N-1 OCP release image (previous minor) for control plane upgrade tests.",
		false,
	)
	RegisterEnvVar(
		"E2E_N1_RELEASE_IMAGE",
		"N-1 minor release image for NodePool previous-release tests.",
		false,
	)
	RegisterEnvVar(
		"E2E_N2_RELEASE_IMAGE",
		"N-2 minor release image for NodePool previous-release tests.",
		false,
	)
	RegisterEnvVar(
		"E2E_N3_RELEASE_IMAGE",
		"N-3 minor release image for AWS NodePool supported-version skew tests.",
		false,
	)
	RegisterEnvVar(
		"E2E_N4_RELEASE_IMAGE",
		"N-4 minor release image for AWS NodePool unsupported-version skew tests.",
		false,
	)
	RegisterEnvVar(
		"E2E_AWS_EBS_KMS_KEY_ARN",
		"AWS KMS key ARN or alias for NodePool root EBS encryption; empty exercises the default EBS encryption key in a separate run.",
		false,
	)
	RegisterEnvVar(
		"E2E_AWS_KMS_KEY_ARN",
		"AWS KMS key ARN supplied to creation of the custom-config HostedCluster for etcd encryption.",
		false,
	)
	RegisterEnvVar(
		"E2E_AWS_KMS_KEY_ALIAS",
		"Optional AWS etcd KMS alias resolved through DescribeKey before custom-config creation; also supply it to run-tests for independent identity verification.",
		false,
	)
	RegisterEnvVarWithDefault(
		"E2E_DESTROY_TIMEOUT",
		"Total destroy-guests retry budget, separate from the per-attempt cluster grace period; positive Go duration.",
		false,
		"2h",
	)
	RegisterEnvVar(
		"AWS_OIDC_S3_BUCKET_NAME",
		"Existing public S3 bucket for automatic suite-owned AWS service-account OIDC discovery and JWKS publication; required unless an external issuer and signing key are supplied.",
		false,
	)
	RegisterEnvVar(
		"AWS_OIDC_S3_CREDENTIALS_FILE",
		"Optional separate AWS S3 credentials/config file for shared OIDC publication and cleanup; falls back to AWS_GUEST_INFRA_CREDENTIALS_FILE.",
		false,
	)
	RegisterEnvVar(
		"AWS_OIDC_S3_REGION",
		"Optional shared OIDC bucket region; falls back to HYPERSHIFT_AWS_REGION.",
		false,
	)
	RegisterEnvVar(
		"E2E_AWS_SHARED_OIDC_ISSUER_URL",
		"Existing AWS IAM service-account OIDC issuer reused by all selected AWS fixtures; pair with E2E_AWS_SHARED_OIDC_SIGNING_KEY_FILE. Does not create or own the provider.",
		false,
	)
	RegisterEnvVar(
		"E2E_AWS_SHARED_OIDC_SIGNING_KEY_FILE",
		"PKCS1 RSA private-key PEM file matching the existing shared AWS OIDC issuer; consumed only by create-guests and never stored in the manifest.",
		false,
	)
	RegisterEnvVar(
		"PROW_JOB_ID",
		"Optional Prow job identifier used to tag AWS resources created by lifecycle tests.",
		false,
	)
	RegisterEnvVar(
		"TEST_CPO_OVERRIDE",
		"Legacy explicit CPO override request; create-guests also requires the dedicated aws-e2e-v2-cpo-overrides test plan.",
		false,
	)
	RegisterEnvVar(
		"E2E_HYPERSHIFT_OPERATOR_NAMESPACE",
		"Optional namespace selecting one already-installed HyperShift operator for read-only CPO override prerequisite validation; empty discovers a unique operator.",
		false,
	)
	RegisterEnvVarWithDefault(
		"E2E_AWS_LICENSED_INSTANCE_TYPE",
		"AWS instance type supporting the licensed Windows AMI test. Requires bare-metal capacity and quota.",
		false,
		"m5.metal",
	)
	RegisterEnvVar(
		"E2E_AZURE_CREDENTIALS_FILE",
		"Path to Azure service principal credentials JSON file for platform-specific tests (auto-repair, disk encryption).",
		false,
	)
	RegisterEnvVar(
		"E2E_AZURE_DISK_ENCRYPTION_SET_ID",
		"Azure DiskEncryptionSet resource ID for disk encryption NodePool tests.",
		false,
	)
	// Karpenter test environment variables
	RegisterEnvVarWithDefault(
		"PULL_SECRET_FILE",
		"Path to pull secret file for release image lookups. Used by Karpenter version field tests.",
		false,
		"",
	)
	// External OIDC test environment variables
	RegisterEnvVar(
		"E2E_EXTERNAL_OIDC_CA_BUNDLE_FILE",
		"Path to the CA bundle file for the External OIDC issuer (Keycloak). Written by the lifecycle PostCreate.",
		false,
	)
	RegisterEnvVar(
		"E2E_EXTERNAL_OIDC_TEST_USERS",
		"Comma-separated list of test users in user:password format for External OIDC testing. Written by the lifecycle PostCreate.",
		false,
	)
	RegisterEnvVar(
		"RUN_KARPENTER_TESTS",
		"When the value is exactly \"true\", it lowers the minimum hosted-cluster version from 4.23 to 4.22.",
		false,
	)
	// TODO: Should this be a capability check?
	RegisterEnvVar(
		"AWS_MULTI_ARCH",
		"Enable multi-arch cluster tests on AWS",
		false,
	)
}
