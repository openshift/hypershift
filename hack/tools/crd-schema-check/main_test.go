package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"sigs.k8s.io/crdify/pkg/config"
	"sigs.k8s.io/crdify/pkg/runner"
	kyaml "sigs.k8s.io/yaml"
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
		name        string
		old         string
		new         string
		wantFailure bool
	}{
		{
			name: "When removing a field, it should warn under repository policy",
			old:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}, {name: "fieldB", fieldType: "string"}}),
			new:  crdWithFields([]fieldSpec{{name: "fieldA", fieldType: "string"}}),
		},
		{
			name: "When removing an enum value, it should warn under repository policy",
			old:  crdWithEnum("status", []string{"Active", "Inactive", "Pending"}),
			new:  crdWithEnum("status", []string{"Active", "Inactive"}),
		},
		{
			name:        "When adding a new required field, it should reject the breaking change",
			wantFailure: true,
			old:         crdWithRequiredFields([]fieldSpec{{name: "fieldA", fieldType: "string"}}, []string{"fieldA"}),
			new:         crdWithRequiredFields([]fieldSpec{{name: "fieldA", fieldType: "string"}, {name: "fieldB", fieldType: "string"}}, []string{"fieldA", "fieldB"}),
		},
		{
			name: "When changing a field type, it should warn under repository policy",
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
			g.Expect(results.HasFailures()).To(Equal(tt.wantFailure))
			if !tt.wantFailure {
				var collected checkResults
				for _, version := range results.SameVersionValidation {
					collectPropertyResults(&collected, "fixture", version.Version, version.PropertyComparisons)
				}
				g.Expect(collected.warnings).NotTo(BeEmpty(), "warn-only policy must still report the schema change")
			}
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

func TestLoadCRDFromCommit(t *testing.T) {
	// Hooks export repository-local Git variables. The fixture must discover its
	// own repository rather than reinitialize the checkout running the hook.
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "GIT_") {
			t.Setenv(key, value[len(key)+1:])
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--initial-branch=main")
	// Avoid inheriting repository hooks and signing configuration in this fixture.
	git("config", "core.hooksPath", filepath.Join(repo, "no-hooks"))
	git("config", "commit.gpgsign", "false")
	git("config", "user.name", "HyperShift Tests")
	git("config", "user.email", "hypershift-tests@redhat.com")
	data := []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: clusters.hypershift.openshift.io
spec:
  versions:
  - name: v1beta1
    schema:
      openAPIV3Schema:
        type: object
`)
	path := "cluster [v1].yaml"
	if err := os.WriteFile(filepath.Join(repo, path), data, 0644); err != nil {
		t.Fatal(err)
	}
	git("add", path)
	git("commit", "-m", "fixture")
	// This is legal to Git, but go-git rejects it with 'branch config: invalid merge'.
	git("config", "branch.unrelated.merge", "not-a-branch-ref")
	hash, err := resolveBaseHash(repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, hash, path string
		missing, invalid bool
	}{
		{"When a committed path contains glob characters, it should read it literally", hash, path, false, false},
		{"When a CRD is new, it should return no base", hash, "missing.yaml", true, false},
		{"When no base is provided, it should return no CRD", "", path, true, false},
		{"When the base hash is invalid, it should fail", "invalid-base", path, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			crd, err := loadCRDFromCommit(repo, tc.hash, tc.path)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid revision accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.missing {
				if crd != nil {
					t.Fatal("expected no base")
				}
			} else if crd == nil || crd.Name != "clusters.hypershift.openshift.io" || len(crd.Spec.Versions) != 1 {
				t.Fatalf("unexpected CRD: %v", crd)
			}
		})
	}
	if _, err := resolveBaseHash(repo, "does-not-exist"); err == nil {
		t.Fatal("missing ref accepted")
	}
	// Exercise the same lookup from a linked worktree.
	worktree := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "--detach", worktree, "HEAD")
	if got, err := resolveBaseHash(worktree, "main"); err != nil || got != hash {
		t.Fatalf("linked worktree: %q, %v", got, err)
	}
}
