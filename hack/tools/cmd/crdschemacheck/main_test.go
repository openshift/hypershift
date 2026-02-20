package main

import (
	"testing"

	"github.com/openshift/crd-schema-checker/pkg/cmd/options"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newCRD(name string, schema *apiextensionsv1.JSONSchemaProps) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "hypershift.openshift.io",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   name,
				Singular: name,
				Kind:     name,
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1beta1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: schema,
					},
					Subresources: &apiextensionsv1.CustomResourceSubresources{
						Status: &apiextensionsv1.CustomResourceSubresourceStatus{},
					},
				},
			},
		},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{
			StoredVersions: []string{"v1beta1"},
		},
	}
}

func mustBuildConfig(t *testing.T) *options.ComparatorConfig {
	t.Helper()
	config, err := buildComparatorConfig()
	if err != nil {
		t.Fatalf("failed to build comparator config: %v", err)
	}
	return config
}

func TestCompareCRDs_WhenFieldIsRemoved_ItShouldDetectBreakingChange(t *testing.T) {
	config := mustBuildConfig(t)

	oldSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"foo": {Type: "string"},
					"bar": {Type: "string"},
				},
			},
			"status": {Type: "object"},
		},
	}

	newSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"foo": {Type: "string"},
					// "bar" removed - breaking change
				},
			},
			"status": {Type: "object"},
		},
	}

	oldCRD := newCRD("testresource", oldSchema)
	newCRD := newCRD("testresource", newSchema)

	errors, _ := compareCRDs("test.yaml", oldCRD, newCRD, config)
	if errors == 0 {
		t.Error("expected breaking change error for field removal, got none")
	}
}

func TestCompareCRDs_WhenEnumValueIsRemoved_ItShouldDetectBreakingChange(t *testing.T) {
	config := mustBuildConfig(t)

	oldSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"mode": {
						Type: "string",
						Enum: []apiextensionsv1.JSON{
							{Raw: []byte(`"Alpha"`)},
							{Raw: []byte(`"Beta"`)},
							{Raw: []byte(`"GA"`)},
						},
					},
				},
			},
			"status": {Type: "object"},
		},
	}

	newSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"mode": {
						Type: "string",
						Enum: []apiextensionsv1.JSON{
							{Raw: []byte(`"Beta"`)},
							{Raw: []byte(`"GA"`)},
							// "Alpha" removed - breaking change
						},
					},
				},
			},
			"status": {Type: "object"},
		},
	}

	oldCRD := newCRD("testresource", oldSchema)
	newCRD := newCRD("testresource", newSchema)

	errors, _ := compareCRDs("test.yaml", oldCRD, newCRD, config)
	if errors == 0 {
		t.Error("expected breaking change error for enum removal, got none")
	}
}

func TestCompareCRDs_WhenNewRequiredFieldIsAdded_ItShouldDetectBreakingChange(t *testing.T) {
	config := mustBuildConfig(t)

	oldSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"foo": {Type: "string"},
				},
			},
			"status": {Type: "object"},
		},
	}

	newSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type:     "object",
				Required: []string{"foo", "bar"},
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"foo": {Type: "string"},
					"bar": {Type: "string"},
				},
			},
			"status": {Type: "object"},
		},
	}

	oldCRD := newCRD("testresource", oldSchema)
	newCRD := newCRD("testresource", newSchema)

	errors, _ := compareCRDs("test.yaml", oldCRD, newCRD, config)
	if errors == 0 {
		t.Error("expected breaking change error for new required field, got none")
	}
}

func TestCompareCRDs_WhenDataTypeChanges_ItShouldDetectBreakingChange(t *testing.T) {
	config := mustBuildConfig(t)

	oldSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"count": {Type: "string"},
				},
			},
			"status": {Type: "object"},
		},
	}

	newSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"count": {Type: "integer"},
				},
			},
			"status": {Type: "object"},
		},
	}

	oldCRD := newCRD("testresource", oldSchema)
	newCRD := newCRD("testresource", newSchema)

	errors, _ := compareCRDs("test.yaml", oldCRD, newCRD, config)
	if errors == 0 {
		t.Error("expected breaking change error for data type change, got none")
	}
}

func TestCompareCRDs_WhenOnlyAdditiveChangesAreMade_ItShouldPass(t *testing.T) {
	config := mustBuildConfig(t)

	oldSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"foo": {Type: "string"},
				},
			},
			"status": {Type: "object"},
		},
	}

	newSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"foo": {Type: "string"},
					"bar": {Type: "string"}, // new optional field - non-breaking
				},
			},
			"status": {Type: "object"},
		},
	}

	oldCRD := newCRD("testresource", oldSchema)
	newCRD := newCRD("testresource", newSchema)

	errors, _ := compareCRDs("test.yaml", oldCRD, newCRD, config)
	if errors != 0 {
		t.Errorf("expected no breaking change errors for additive change, got %d", errors)
	}
}

func TestCompareCRDs_WhenEnumValueIsAdded_ItShouldPass(t *testing.T) {
	config := mustBuildConfig(t)

	oldSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"mode": {
						Type: "string",
						Enum: []apiextensionsv1.JSON{
							{Raw: []byte(`"Alpha"`)},
							{Raw: []byte(`"Beta"`)},
						},
					},
				},
			},
			"status": {Type: "object"},
		},
	}

	newSchema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"mode": {
						Type: "string",
						Enum: []apiextensionsv1.JSON{
							{Raw: []byte(`"Alpha"`)},
							{Raw: []byte(`"Beta"`)},
							{Raw: []byte(`"GA"`)}, // new enum value - non-breaking
						},
					},
				},
			},
			"status": {Type: "object"},
		},
	}

	oldCRD := newCRD("testresource", oldSchema)
	newCRD := newCRD("testresource", newSchema)

	errors, _ := compareCRDs("test.yaml", oldCRD, newCRD, config)
	if errors != 0 {
		t.Errorf("expected no breaking change errors for additive enum change, got %d", errors)
	}
}

func TestIsCRDFile_WhenValidCRD_ItShouldReturnTrue(t *testing.T) {
	data := []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: test.example.com
`)
	if !isCRDFile(data) {
		t.Error("expected isCRDFile to return true for valid CRD YAML")
	}
}

func TestIsCRDFile_WhenNotCRD_ItShouldReturnFalse(t *testing.T) {
	data := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`)
	if isCRDFile(data) {
		t.Error("expected isCRDFile to return false for non-CRD YAML")
	}
}

func TestIsCRDFile_WhenInvalidYAML_ItShouldReturnFalse(t *testing.T) {
	data := []byte(`not: valid: yaml: [`)
	if isCRDFile(data) {
		t.Error("expected isCRDFile to return false for invalid YAML")
	}
}

func TestHasVersionedSchema_WhenSchemaPresent_ItShouldReturnTrue(t *testing.T) {
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name: "v1",
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
					},
				},
			},
		},
	}
	if !hasVersionedSchema(crd) {
		t.Error("expected hasVersionedSchema to return true when schema is present")
	}
}

func TestHasVersionedSchema_WhenNoSchema_ItShouldReturnFalse(t *testing.T) {
	crd := &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1"},
			},
		},
	}
	if hasVersionedSchema(crd) {
		t.Error("expected hasVersionedSchema to return false when no schema is present")
	}
}

func TestBuildComparatorConfig_WhenCalled_ItShouldDisableKALComparators(t *testing.T) {
	config, err := buildComparatorConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	enabledNames := make(map[string]bool)
	for _, name := range config.ComparatorNames {
		enabledNames[name] = true
	}

	// These should be disabled (enforced by KAL instead).
	for _, disabled := range comparatorsDisabledByKAL {
		if enabledNames[disabled] {
			t.Errorf("expected comparator %q to be disabled (enforced by KAL), but it is enabled", disabled)
		}
	}

	// These should be enabled.
	expectedEnabled := []string{
		"NoFieldRemoval",
		"NoEnumRemoval",
		"NoNewRequiredFields",
		"NoDataTypeChange",
		"MustHaveStatus",
		"ListsMustHaveSSATags",
		"MustNotExceedCostBudget",
	}
	for _, name := range expectedEnabled {
		if !enabledNames[name] {
			t.Errorf("expected comparator %q to be enabled, but it is not", name)
		}
	}
}
