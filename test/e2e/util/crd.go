package util

import (
	"context"
	"fmt"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// HasFieldInCRDSchema checks if a field path exists in the CRD schema by recursively traversing
// the JSONSchemaProps. The fieldPath should be dot-separated (e.g., "spec.platform.gcp").
func HasFieldInCRDSchema(ctx context.Context, client crclient.Client, crdName, fieldPath string) (bool, error) {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := client.Get(ctx, crclient.ObjectKey{Name: crdName}, crd); err != nil {
		return false, fmt.Errorf("failed to get CRD %s: %w", crdName, err)
	}

	// Find the served version (prefer v1beta1 if available, otherwise use the first served version)
	var version *apiextensionsv1.CustomResourceDefinitionVersion
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Served {
			version = &crd.Spec.Versions[i]
			if version.Name == "v1beta1" {
				break
			}
		}
	}
	if version == nil || version.Schema == nil || version.Schema.OpenAPIV3Schema == nil {
		return false, fmt.Errorf("no valid schema found for CRD %s", crdName)
	}

	// Split the field path into parts
	pathParts := strings.Split(fieldPath, ".")
	return hasFieldInSchema(version.Schema.OpenAPIV3Schema, pathParts, 0), nil
}

// hasFieldInSchema recursively checks if a field path exists in the JSONSchemaProps
func hasFieldInSchema(schema *apiextensionsv1.JSONSchemaProps, pathParts []string, index int) bool {
	if schema == nil || index >= len(pathParts) {
		return index == len(pathParts)
	}

	currentPart := pathParts[index]

	// Check properties first
	if schema.Properties != nil {
		if prop, exists := schema.Properties[currentPart]; exists {
			if index == len(pathParts)-1 {
				// This is the last part, field exists
				return true
			}
			// Recurse into the property
			return hasFieldInSchema(&prop, pathParts, index+1)
		}
	}

	// Bridge into array items before falling back to the combinators.
	if schema.Items != nil {
		if schema.Items.Schema != nil && hasFieldInSchema(schema.Items.Schema, pathParts, index) {
			return true
		}
		for i := range schema.Items.JSONSchemas {
			if hasFieldInSchema(&schema.Items.JSONSchemas[i], pathParts, index) {
				return true
			}
		}
	}

	// Check AllOf, AnyOf, OneOf - these can contain the field
	for i := range schema.AllOf {
		if hasFieldInSchema(&schema.AllOf[i], pathParts, index) {
			return true
		}
	}
	for i := range schema.AnyOf {
		if hasFieldInSchema(&schema.AnyOf[i], pathParts, index) {
			return true
		}
	}
	for i := range schema.OneOf {
		if hasFieldInSchema(&schema.OneOf[i], pathParts, index) {
			return true
		}
	}

	// Check if there's a $ref that we need to follow
	// Note: In CRDs, $ref typically points to definitions within the same schema
	// For simplicity, we check properties which is the most common case
	return false
}
