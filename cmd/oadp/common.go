package oadp

import (
	"fmt"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"sigs.k8s.io/yaml"
)

// renderYAMLObject renders an unstructured object as YAML to stdout with proper formatting
func renderYAMLObject(obj *unstructured.Unstructured) error {
	// Convert to YAML
	yamlBytes, err := yaml.Marshal(obj.Object)
	if err != nil {
		return fmt.Errorf("failed to marshal object to YAML: %w", err)
	}

	// Output to STDOUT with YAML document separator
	_, err = os.Stdout.WriteString("\n---\n")
	if err != nil {
		return fmt.Errorf("failed to write YAML separator to stdout: %w", err)
	}
	_, err = os.Stdout.Write(yamlBytes)
	if err != nil {
		return fmt.Errorf("failed to write YAML to stdout: %w", err)
	}
	return nil
}

// getDefaultResourcesForPlatform returns the default resource list based on the platform
func getDefaultResourcesForPlatform(platform string) []string {
	// Get platform-specific resources, default to AWS if platform is unknown
	platformResources, exists := platformResourceMap[strings.ToUpper(platform)]
	if !exists {
		platformResources = awsResources
	}

	// Combine base and platform-specific resources
	result := make([]string, len(baseResources)+len(platformResources))
	copy(result, baseResources)
	copy(result[len(baseResources):], platformResources)

	return result
}

// buildIncludedNamespaces builds the list of namespaces to include in backup/schedule/restore operations
// Always includes HC-namespace and HCP-namespace, and then adds any additional namespaces specified
func buildIncludedNamespaces(hcNamespace, hcName string, additionalNamespaces []string) []string {
	// Always include HC and HCP namespaces
	namespaces := []string{
		hcNamespace,
		fmt.Sprintf("%s-%s", hcNamespace, hcName),
	}

	// Add any additional namespaces specified by the user
	if len(additionalNamespaces) > 0 {
		namespaces = append(namespaces, additionalNamespaces...)
	}

	return namespaces
}
