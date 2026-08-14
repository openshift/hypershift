package main

import (
	"testing"

	. "github.com/onsi/gomega"
	kyaml "sigs.k8s.io/yaml"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"sigs.k8s.io/crdify/pkg/config"
	"sigs.k8s.io/crdify/pkg/runner"
)

func TestIsCRDYAML(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		yaml     string
		expected bool
	}{
		{
			name: "When given a valid CRD it should return true",
			yaml: `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: tests.example.com`,
			expected: true,
		},
		{
			name: "When given a non-CRD resource it should return false",
			yaml: `apiVersion: v1
kind: ConfigMap
metadata:
  name: test`,
			expected: false,
		},
		{
			name:     "When given invalid YAML it should return false",
			yaml:     `{not valid yaml`,
			expected: false,
		},
		{
			name:     "When given empty input it should return false",
			yaml:     "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(isCRDYAML([]byte(tt.yaml))).To(Equal(tt.expected))
		})
	}
}

func TestSkipFeatureGateVariant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		{
			name:     "When given a Default variant it should skip",
			filename: "hostedclusters-Hypershift-Default.crd.yaml",
			expected: true,
		},
		{
			name:     "When given a TechPreviewNoUpgrade variant it should skip",
			filename: "hostedclusters-Hypershift-TechPreviewNoUpgrade.crd.yaml",
			expected: true,
		},
		{
			name:     "When given a CustomNoUpgrade variant it should not skip",
			filename: "hostedclusters-Hypershift-CustomNoUpgrade.crd.yaml",
			expected: false,
		},
		{
			name:     "When given a plain CRD file it should not skip",
			filename: "awsendpointservices.crd.yaml",
			expected: false,
		},
		{
			name:     "When given a non-CRD file it should not skip",
			filename: "doc.go",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(skipFeatureGateVariant(tt.filename)).To(Equal(tt.expected))
		})
	}
}

func TestFilterVersionsWithSchema(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		yaml             string
		expectedVersions int
	}{
		{
			name: "When all versions have schemas it should keep all versions",
			yaml: baseCRDWithVersions([]versionSpec{
				{name: "v1", hasSchema: true},
				{name: "v1beta1", hasSchema: true},
			}),
			expectedVersions: 2,
		},
		{
			name: "When some versions lack schemas it should filter them out",
			yaml: baseCRDWithVersions([]versionSpec{
				{name: "v1", hasSchema: true},
				{name: "v1beta1", hasSchema: false},
			}),
			expectedVersions: 1,
		},
		{
			name: "When no versions have schemas it should return zero versions",
			yaml: baseCRDWithVersions([]versionSpec{
				{name: "v1", hasSchema: false},
			}),
			expectedVersions: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			crd, err := readCRD([]byte(tt.yaml))
			g.Expect(err).NotTo(HaveOccurred())
			result := filterVersionsWithSchema(crd)
			g.Expect(result.Spec.Versions).To(HaveLen(tt.expectedVersions))
		})
	}
}

func newCrdifyRunner(t *testing.T) *runner.Runner {
	t.Helper()
	g := NewWithT(t)
	cfg, err := config.Load("crdify-config.yaml")
	g.Expect(err).NotTo(HaveOccurred())
	r, err := runner.New(cfg, runner.DefaultRegistry())
	g.Expect(err).NotTo(HaveOccurred())
	return r
}

func TestCompareCRDs_NonBreakingChanges(t *testing.T) {
	t.Parallel()
	crdifyRunner := newCrdifyRunner(t)

	tests := []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "When adding a new optional field it should pass",
			old:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}}),
			new:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}, {name: "fieldB", fieldType: "string"}}),
		},
		{
			name: "When the CRD is unchanged it should pass",
			old:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}}),
			new:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}}),
		},
		{
			name: "When adding an enum value it should pass",
			old:  crdWithEnum("status", []string{"Active", "Inactive"}),
			new:  crdWithEnum("status", []string{"Active", "Inactive", "Pending"}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			oldCRD := mustUnmarshalCRD(t, tt.old)
			newCRD := mustUnmarshalCRD(t, tt.new)

			results := compareCRDs(oldCRD, newCRD, crdifyRunner)
			g.Expect(results.HasFailures()).To(BeFalse(), "expected no breaking changes but got failures")
		})
	}
}

func TestCompareCRDs_BreakingChanges(t *testing.T) {
	t.Parallel()
	crdifyRunner := newCrdifyRunner(t)

	tests := []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "When removing a field it should detect the breaking change",
			old:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}, {name: "fieldB", fieldType: "string"}}),
			new:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}}),
		},
		{
			name: "When removing an enum value it should detect the breaking change",
			old:  crdWithEnum("status", []string{"Active", "Inactive", "Pending"}),
			new:  crdWithEnum("status", []string{"Active", "Inactive"}),
		},
		{
			name: "When adding a new required field it should detect the breaking change",
			old:  crdWithRequiredFields([]fieldSpec{{name: "fieldA", fieldType: "string"}}, []string{"fieldA"}),
			new:  crdWithRequiredFields([]fieldSpec{{name: "fieldA", fieldType: "string"}, {name: "fieldB", fieldType: "string"}}, []string{"fieldA", "fieldB"}),
		},
		{
			name: "When changing a field type it should detect the breaking change",
			old:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}}),
			new:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "integer"}}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			oldCRD := mustUnmarshalCRD(t, tt.old)
			newCRD := mustUnmarshalCRD(t, tt.new)

			results := compareCRDs(oldCRD, newCRD, crdifyRunner)
			g.Expect(results.HasFailures()).To(BeTrue(), "expected breaking change to be detected")
		})
	}
}

func TestCompareCRDs_NewCRDWithNilOld(t *testing.T) {
	t.Parallel()
	crdifyRunner := newCrdifyRunner(t)

	t.Run("When comparing a new CRD with no old version it should not report failures", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		newCRD := mustUnmarshalCRD(t, crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}}))

		results := compareCRDs(nil, newCRD, crdifyRunner)
		g.Expect(results.HasFailures()).To(BeFalse(), "unexpected failures for new CRD with nil old")
	})
}

// mustUnmarshalCRD unmarshals YAML to a CRD, failing the test on error.
func mustUnmarshalCRD(t *testing.T, yamlData string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := kyaml.Unmarshal([]byte(yamlData), crd); err != nil {
		t.Fatalf("failed to unmarshal test CRD: %v", err)
	}
	return crd
}

// Test helpers for generating CRD YAML.

type fieldSpec struct {
	name      string
	fieldType string
}

type versionSpec struct {
	name      string
	hasSchema bool
}

func baseCRDWithVersions(versions []versionSpec) string {
	yaml := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: tests.example.com
spec:
  group: example.com
  names:
    kind: Test
    listKind: TestList
    plural: tests
    singular: test
  scope: Namespaced
  versions:
`
	for _, v := range versions {
		yaml += "  - name: " + v.name + "\n"
		yaml += "    served: true\n"
		yaml += "    storage: true\n"
		if v.hasSchema {
			yaml += `    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              name:
                type: string
          status:
            type: object
        required:
        - spec
    subresources:
      status: {}
`
		}
	}
	return yaml
}

func crdWithFields(fields []fieldSpec) string {
	yaml := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: tests.example.com
spec:
  group: example.com
  names:
    kind: Test
    listKind: TestList
    plural: tests
    singular: test
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
`
	for _, f := range fields {
		yaml += "              " + f.name + ":\n"
		yaml += "                type: " + f.fieldType + "\n"
	}
	yaml += `          status:
            type: object
        required:
        - spec
    subresources:
      status: {}
`
	return yaml
}

func crdWithRequiredFields(fields []fieldSpec, required []string) string {
	yaml := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: tests.example.com
spec:
  group: example.com
  names:
    kind: Test
    listKind: TestList
    plural: tests
    singular: test
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
`
	for _, f := range fields {
		yaml += "              " + f.name + ":\n"
		yaml += "                type: " + f.fieldType + "\n"
	}
	if len(required) > 0 {
		yaml += "            required:\n"
		for _, r := range required {
			yaml += "            - " + r + "\n"
		}
	}
	yaml += `          status:
            type: object
        required:
        - spec
    subresources:
      status: {}
`
	return yaml
}

func crdWithEnum(fieldName string, values []string) string {
	yaml := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: tests.example.com
spec:
  group: example.com
  names:
    kind: Test
    listKind: TestList
    plural: tests
    singular: test
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              ` + fieldName + `:
                type: string
                enum:
`
	for _, v := range values {
		yaml += "                - " + v + "\n"
	}
	yaml += `          status:
            type: object
        required:
        - spec
    subresources:
      status: {}
`
	return yaml
}
