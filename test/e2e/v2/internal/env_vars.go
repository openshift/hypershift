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
		"E2E_AZURE_CREDENTIALS_FILE",
		"Path to Azure service principal credentials JSON file for platform-specific tests (auto-repair, disk encryption).",
		false,
	)
	RegisterEnvVar(
		"E2E_AZURE_DISK_ENCRYPTION_SET_ID",
		"Azure DiskEncryptionSet resource ID for disk encryption NodePool tests.",
		false,
	)
}
