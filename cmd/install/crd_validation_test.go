package install

import (
	"bytes"
	"io"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/install/assets"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// hasFieldInSchema recursively checks if a field path exists in the JSONSchemaProps
// visited is used to prevent infinite loops when traversing circular schema references
// maxDepth limits recursion depth to prevent stack overflow
func hasFieldInSchema(schema *apiextensionsv1.JSONSchemaProps, pathParts []string, index int, visited map[*apiextensionsv1.JSONSchemaProps]bool, maxDepth int) bool {
	if schema == nil || index >= len(pathParts) || maxDepth <= 0 {
		return index == len(pathParts)
	}

	// Prevent infinite loops by tracking visited schemas
	if visited == nil {
		visited = make(map[*apiextensionsv1.JSONSchemaProps]bool)
	}
	if visited[schema] {
		return false
	}
	visited[schema] = true

	currentPart := pathParts[index]

	// Check properties - this is the primary path for field access
	if schema.Properties != nil {
		if prop, exists := schema.Properties[currentPart]; exists {
			if index == len(pathParts)-1 {
				// This is the last part, field exists
				return true
			}
			// Recurse into the property with a fresh visited map for the nested path
			// This allows us to traverse the same schema at different path levels
			return hasFieldInSchema(&prop, pathParts, index+1, make(map[*apiextensionsv1.JSONSchemaProps]bool), maxDepth-1)
		}
	}

	// Check AllOf, AnyOf, OneOf - these can contain the field through schema composition
	// We only check these if we haven't found the property yet
	for i := range schema.AllOf {
		if hasFieldInSchema(&schema.AllOf[i], pathParts, index, visited, maxDepth-1) {
			return true
		}
	}
	for i := range schema.AnyOf {
		if hasFieldInSchema(&schema.AnyOf[i], pathParts, index, visited, maxDepth-1) {
			return true
		}
	}
	for i := range schema.OneOf {
		if hasFieldInSchema(&schema.OneOf[i], pathParts, index, visited, maxDepth-1) {
			return true
		}
	}

	return false
}

// TestHostedClusterSchedulerProfileCustomizationsDynamicResourceAllocationFieldExists verifies that
// the field spec.configuration.scheduler.profileCustomizations.dynamicResourceAllocation exists in the
// generated HostedCluster CRD manifest. When the field does not exist, it should fail.
// This is to ensure that a bump of the openshift/api does not remove this field from HyperShift, since it
// needs to be deprecated first.
func TestHostedClusterSchedulerProfileCustomizationsDynamicResourceAllocationFieldExists(t *testing.T) {
	// Field path in the CRD schema (using camelCase as per JSON schema conventions)
	fieldPath := "spec.configuration.scheduler.profileCustomizations.dynamicResourceAllocation"
	pathParts := strings.Split(fieldPath, ".")

	// Test all HostedCluster CRD variants
	crdPaths := []string{
		"hypershift-operator/zz_generated.crd-manifests/hostedclusters-Hypershift-Default.crd.yaml",
		"hypershift-operator/zz_generated.crd-manifests/hostedclusters-Hypershift-TechPreviewNoUpgrade.crd.yaml",
		"hypershift-operator/zz_generated.crd-manifests/hostedclusters-Hypershift-CustomNoUpgrade.crd.yaml",
	}

	for _, crdPath := range crdPaths {
		t.Run(crdPath, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Load the CRD directly from embedded filesystem to avoid parsing all CRDs
			f, err := assets.CRDS.Open(crdPath)
			g.Expect(err).ToNot(HaveOccurred(), "Failed to open CRD file %s", crdPath)
			defer f.Close()

			crdBytes, err := io.ReadAll(f)
			g.Expect(err).ToNot(HaveOccurred(), "Failed to read CRD file %s", crdPath)

			// Strip leading YAML document separator if present (same as assets.getCustomResourceDefinition)
			repaired := bytes.Replace(crdBytes, []byte("\n---\n"), []byte(""), 1)

			var hostedClusterCRD apiextensionsv1.CustomResourceDefinition
			err = yaml.NewYAMLOrJSONDecoder(bytes.NewReader(repaired), 100).Decode(&hostedClusterCRD)
			g.Expect(err).ToNot(HaveOccurred(), "Failed to decode CRD file %s", crdPath)

			// Find the v1beta1 version schema
			var version *apiextensionsv1.CustomResourceDefinitionVersion
			for i := range hostedClusterCRD.Spec.Versions {
				if hostedClusterCRD.Spec.Versions[i].Served && hostedClusterCRD.Spec.Versions[i].Name == "v1beta1" {
					version = &hostedClusterCRD.Spec.Versions[i]
					break
				}
			}

			g.Expect(version).ToNot(BeNil(), "v1beta1 version not found in CRD %s", crdPath)
			g.Expect(version.Schema).ToNot(BeNil(), "Schema not found in CRD %s", crdPath)
			g.Expect(version.Schema.OpenAPIV3Schema).ToNot(BeNil(), "OpenAPIV3Schema not found in CRD %s", crdPath)

			// Check if the field exists in the schema
			// Use a reasonable depth limit (50 should be more than enough for our field path)
			fieldExists := hasFieldInSchema(version.Schema.OpenAPIV3Schema, pathParts, 0, nil, 50)
			g.Expect(fieldExists).To(BeTrue(), "Field %s does not exist in CRD %s", fieldPath, crdPath)
		})
	}
}
