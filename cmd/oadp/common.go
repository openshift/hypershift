package oadp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	utilroute "github.com/openshift/hypershift/support/util"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/validation"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
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

// isKubeVirtPlatform checks if the given platform is KubeVirt (case-insensitive).
func isKubeVirtPlatform(platform string) bool {
	return strings.ToUpper(platform) == "KUBEVIRT"
}

// applyPlatformBackupSpec adds platform-specific fields to a backup spec map.
// For KubeVirt, this adds a labelSelector to exclude RHCOS boot image PVCs
// since they are recreated when new VMs are provisioned post-restore.
func applyPlatformBackupSpec(spec map[string]interface{}, platform string) {
	if isKubeVirtPlatform(platform) {
		spec["labelSelector"] = map[string]interface{}{
			"matchExpressions": []interface{}{
				map[string]interface{}{
					"key":      v1beta1.IsKubeVirtRHCOSVolumeLabelName,
					"operator": "DoesNotExist",
				},
			},
		}
	}
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

// generateName creates a name using the format: {hcName}-{hcNamespace}-{randomSuffix}.
// If the name is too long, it uses utils.ShortenName to ensure it doesn't exceed 63 characters.
func generateName(hcName, hcNamespace string) string {
	randomSuffix := utilrand.String(6)
	baseName := fmt.Sprintf("%s-%s", hcName, hcNamespace)
	return utilroute.ShortenName(baseName, randomSuffix, validation.DNS1123LabelMaxLength)
}

// GenerateResourcePolicyName creates a deterministic resource policy ConfigMap name
// using the format: {hcName}-{hcNamespace}-{hashSuffix}.
// The suffix is derived from a SHA-256 hash of the inputs so the same HC always
// produces the same ConfigMap name, avoiding orphaned ConfigMaps on retries.
func GenerateResourcePolicyName(hcName, hcNamespace string) string {
	baseName := fmt.Sprintf("%s-%s", hcName, hcNamespace)
	hash := sha256.Sum256([]byte(baseName))
	hashSuffix := hex.EncodeToString(hash[:])[:6]
	return utilroute.ShortenName(baseName, hashSuffix, validation.DNS1123LabelMaxLength)
}

// GenerateResourcePolicyConfigMap creates a ConfigMap with a Velero volume policy that skips
// PVCs labeled with the KubeVirt RHCOS boot image label.
func GenerateResourcePolicyConfigMap(name, namespace string) *unstructured.Unstructured {
	policiesYAML := fmt.Sprintf(`version: v1
volumePolicies:
  - conditions:
      pvcLabels:
        %s: "true"
    action:
      type: skip
`, v1beta1.IsKubeVirtRHCOSVolumeLabelName)

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"data": map[string]interface{}{
				"policies.yaml": policiesYAML,
			},
		},
	}
}

// applyResourcePolicy sets the resourcePolicy field in a backup spec to reference a ConfigMap by name.
func applyResourcePolicy(spec map[string]interface{}, configMapName string) {
	spec["resourcePolicy"] = map[string]interface{}{
		"kind": "configmap",
		"name": configMapName,
	}
}

// maybeGenerateResourcePolicy generates a resource policy ConfigMap for KubeVirt platform
// and applies the resourcePolicy reference to the given spec. Returns nil for non-KubeVirt platforms.
func maybeGenerateResourcePolicy(platform, hcName, hcNamespace, oadpNamespace string, spec map[string]interface{}) *unstructured.Unstructured {
	if !isKubeVirtPlatform(platform) {
		return nil
	}
	configMapName := GenerateResourcePolicyName(hcName, hcNamespace)
	applyResourcePolicy(spec, configMapName)
	return GenerateResourcePolicyConfigMap(configMapName, oadpNamespace)
}

// createResourcePolicyConfigMap creates the resource policy ConfigMap in the cluster if it is non-nil.
// If the ConfigMap already exists, it is treated as a no-op for idempotency.
func createResourcePolicyConfigMap(ctx context.Context, c client.Client, resourcePolicyCM *unstructured.Unstructured, oadpNamespace string, log logr.Logger) error {
	if resourcePolicyCM == nil {
		return nil
	}
	log.Info("Creating resource policy ConfigMap...", "name", resourcePolicyCM.GetName())
	if err := c.Create(ctx, resourcePolicyCM); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Info("Resource policy ConfigMap already exists, skipping creation", "name", resourcePolicyCM.GetName(), "namespace", oadpNamespace)
			return nil
		}
		return fmt.Errorf("failed to create resource policy ConfigMap: %w", err)
	}
	log.Info("Resource policy ConfigMap created successfully", "name", resourcePolicyCM.GetName(), "namespace", oadpNamespace)
	return nil
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
