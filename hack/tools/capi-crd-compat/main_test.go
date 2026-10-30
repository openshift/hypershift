package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"sigs.k8s.io/yaml"
)

const configPath = "../../capi-compat/ibmcloud/compatibility.yaml"

func readManifest(t *testing.T) manifest {
	t.Helper()
	var config manifest
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := decode(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func writeYAML(t *testing.T, path string, value any) {
	t.Helper()
	data, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func fixtures(t *testing.T, generated bool) string {
	t.Helper()
	dir := t.TempDir()
	files, err := filepath.Glob("testdata/*.yaml")
	if err != nil || len(files) != 9 {
		t.Fatalf("fixtures: %v (%d)", err, len(files))
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := decode(data, &crd); err != nil {
			t.Fatal(err)
		}
		if generated && strings.HasPrefix(crd.Spec.Names.Plural, "ibmpowervs") {
			crd.Spec.Versions = crd.Spec.Versions[1:]
			crd.Spec.Versions[0].Deprecated = true
			warning := "upstream deprecation"
			crd.Spec.Versions[0].DeprecationWarning = &warning
			var removeS1122 func(*apiextensionsv1.JSONSchemaProps)
			removeS1122 = func(schema *apiextensionsv1.JSONSchemaProps) {
				var values []apiextensionsv1.JSON
				for _, value := range schema.Enum {
					if string(value.Raw) != `"s1122"` {
						values = append(values, value)
					}
				}
				schema.Enum = values
				for name, child := range schema.Properties {
					removeS1122(&child)
					schema.Properties[name] = child
				}
			}
			removeS1122(crd.Spec.Versions[0].Schema.OpenAPIV3Schema)
		}
		writeYAML(t, filepath.Join(dir, filepath.Base(file)), crd)
	}
	return dir
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[file.Name()] = string(data)
	}
	return result
}

func TestRun(t *testing.T) {
	t.Run("When generated schemas need compatibility, it should restore policies and be idempotent", func(t *testing.T) {
		dir := fixtures(t, true)
		if err := run(configPath, dir); err != nil {
			t.Fatal(err)
		}
		first := snapshot(t, dir)
		if err := run(configPath, dir); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, snapshot(t, dir)) {
			t.Fatal("second pass changed output")
		}
		baseline := snapshot(t, fixtures(t, false))
		for file, data := range first {
			var actual, expected apiextensionsv1.CustomResourceDefinition
			if err := decode([]byte(data), &actual); err != nil {
				t.Fatal(err)
			}
			if err := decode([]byte(baseline[file]), &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Errorf("compatibility differs from baseline: %s", file)
			}
		}
	})
	for _, testCase := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"When a CRD is missing, it should reject without writing", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "infrastructure.cluster.x-k8s.io_ibmpowervsclusters.yaml")); err != nil {
				t.Fatal(err)
			}
		}},
		{"When a CRD is malformed, it should reject without writing", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "infrastructure.cluster.x-k8s.io_ibmvpcmachinetemplates.yaml"), []byte("spec: ["), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"When a schema is only an object, it should reject without writing", func(t *testing.T, dir string) {
			file := filepath.Join(dir, "infrastructure.cluster.x-k8s.io_ibmvpcmachinetemplates.yaml")
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var crd apiextensionsv1.CustomResourceDefinition
			if err := decode(data, &crd); err != nil {
				t.Fatal(err)
			}
			crd.Spec.Versions[0].Schema.OpenAPIV3Schema = &apiextensionsv1.JSONSchemaProps{Type: "object"}
			writeYAML(t, file, crd)
		}},
		{"When a CRD is duplicated, it should reject without writing", func(t *testing.T, dir string) {
			data, err := os.ReadFile(filepath.Join(dir, "infrastructure.cluster.x-k8s.io_ibmpowervsclusters.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "infrastructure.cluster.x-k8s.io_ibmvpcmachinetemplates.yaml"), data, 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"When a CRD is unknown, it should reject without writing", func(t *testing.T, dir string) {
			file := filepath.Join(dir, "infrastructure.cluster.x-k8s.io_ibmvpcmachinetemplates.yaml")
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.ReplaceAll(data, []byte("ibmvpcmachinetemplates.infrastructure"), []byte("unknown.infrastructure"))
			if err := os.WriteFile(file, data, 0644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dir := fixtures(t, true)
			testCase.mutate(t, dir)
			before := snapshot(t, dir)
			if err := run(configPath, dir); err == nil {
				t.Fatal("expected error")
			}
			if !reflect.DeepEqual(before, snapshot(t, dir)) {
				t.Fatal("failed run mutated directory")
			}
		})
	}
	t.Run("When installed assets are regenerated, it should preserve every baseline schema constraint", func(t *testing.T) {
		installed := "../../../cmd/install/assets/crds/cluster-api-provider-ibmcloud"
		config := readManifest(t)
		if err := config.validate(); err != nil {
			t.Fatal(err)
		}
		for file, baseline := range snapshot(t, "testdata") {
			data, err := os.ReadFile(filepath.Join(installed, file))
			if err != nil {
				t.Fatal(err)
			}
			var oldCRD, newCRD apiextensionsv1.CustomResourceDefinition
			if err := decode([]byte(baseline), &oldCRD); err != nil {
				t.Fatal(err)
			}
			if err := decode(data, &newCRD); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(oldCRD.Spec.Conversion, newCRD.Spec.Conversion) || !reflect.DeepEqual(oldCRD.Labels, newCRD.Labels) {
				t.Errorf("conversion/labels changed: %s", file)
			}
			if len(oldCRD.Spec.Versions) != len(newCRD.Spec.Versions) {
				t.Fatalf("version inventory changed: %s", file)
			}
			for index, oldVersion := range oldCRD.Spec.Versions {
				newVersion := newCRD.Spec.Versions[index]
				if oldVersion.Name != newVersion.Name || oldVersion.Served != newVersion.Served || oldVersion.Storage != newVersion.Storage || oldVersion.Deprecated != newVersion.Deprecated || !reflect.DeepEqual(oldVersion.DeprecationWarning, newVersion.DeprecationWarning) {
					t.Errorf("version policy changed: %s", file)
				}
				if strings.Contains(file, "ibmpowervs") && oldVersion.Name == "v1beta1" && !reflect.DeepEqual(oldVersion, newVersion) {
					t.Errorf("legacy entry changed: %s", file)
				}
				oldData, err := json.Marshal(oldVersion.Schema)
				if err != nil {
					t.Fatal(err)
				}
				newData, err := json.Marshal(newVersion.Schema)
				if err != nil {
					t.Fatal(err)
				}
				var oldSchema, newSchema any
				if err := json.Unmarshal(oldData, &oldSchema); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(newData, &newSchema); err != nil {
					t.Fatal(err)
				}
				compareSchema(t, file+"/"+oldVersion.Name, oldSchema, newSchema)
			}
		}
	})
}

func compareSchema(t *testing.T, path string, oldValue, newValue any) {
	t.Helper()
	if oldMap, ok := oldValue.(map[string]any); ok {
		newMap, ok := newValue.(map[string]any)
		if !ok {
			t.Errorf("removed schema %s", path)
			return
		}
		for key, value := range oldMap {
			if key == "description" {
				continue
			}
			compareSchema(t, path+"/"+key, value, newMap[key])
		}
		for key := range newMap {
			if _, exists := oldMap[key]; !exists && key != "description" && !strings.HasSuffix(path, "/properties") {
				t.Errorf("new constraint %s/%s", path, key)
			}
		}
		return
	}
	if !reflect.DeepEqual(oldValue, newValue) {
		t.Errorf("changed constraint %s: %v -> %v", path, oldValue, newValue)
	}
}

func TestManifest_Validate(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*manifest)
	}{
		{"When provenance is missing, it should reject", func(config *manifest) { config.Source = "" }},
		{"When a policy is missing, it should reject", func(config *manifest) { config.CRDs = config.CRDs[1:] }},
		{"When a policy is duplicated, it should reject", func(config *manifest) { config.CRDs[1] = config.CRDs[0] }},
		{"When a policy is unknown, it should reject", func(config *manifest) { config.CRDs[0].Name = "unknown" }},
		{"When a legacy schema is missing, it should reject", func(config *manifest) { config.CRDs[0].Versions[0].Schema = nil }},
		{"When storage changes, it should reject", func(config *manifest) { config.CRDs[0].Versions[0].Storage = true }},
		{"When v1beta3 is requested, it should reject", func(config *manifest) { config.CRDs[0].Versions[1].Name = "v1beta3" }},
		{"When an enum policy is missing, it should reject", func(config *manifest) { config.CRDs[3].Enums = nil }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			config := readManifest(t)
			testCase.mutate(&config)
			if err := config.validate(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	config := readManifest(t)
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRule_Apply(t *testing.T) {
	config := readManifest(t)
	entry := config.CRDs[3]
	for _, testCase := range []struct {
		name   string
		mutate func(*apiextensionsv1.CustomResourceDefinition)
	}{
		{"When versions are duplicated, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) {
			crd.Spec.Versions = append(crd.Spec.Versions, crd.Spec.Versions[1])
		}},
		{"When v1beta2 is missing, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Spec.Versions = crd.Spec.Versions[:1] }},
		{"When v1beta3 appears, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Spec.Versions[1].Name = "v1beta3" }},
		{"When the schema is missing, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Spec.Versions[1].Schema = nil }},
		{"When the legacy schema conflicts, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) {
			crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Type = "string"
		}},
		{"When the enum path is missing, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) {
			delete(crd.Spec.Versions[1].Schema.OpenAPIV3Schema.Properties, "spec")
		}},
		{"When identity changes, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Spec.Group = "unknown" }},
		{"When the CRD name changes, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Name = "unknown" }},
		{"When the CRD API version changes, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.APIVersion = "apiextensions.k8s.io/v1beta1" }},
		{"When the CRD kind changes, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Kind = "Unknown" }},
		{"When the resource scope changes, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Spec.Scope = apiextensionsv1.ClusterScoped }},
		{"When the resource plural changes, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Spec.Names.Plural = "unknown" }},
		{"When the resource kind is empty, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Spec.Names.Kind = "" }},
		{"When the resource kind is incorrect, it should reject", func(crd *apiextensionsv1.CustomResourceDefinition) { crd.Spec.Names.Kind = "IBMPowerVSCluster" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/infrastructure.cluster.x-k8s.io_ibmpowervsmachines.yaml")
			if err != nil {
				t.Fatal(err)
			}
			var crd apiextensionsv1.CustomResourceDefinition
			if err := decode(data, &crd); err != nil {
				t.Fatal(err)
			}
			testCase.mutate(&crd)
			if err := entry.apply(&crd); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestDecode(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		input string
	}{
		{"When YAML is malformed, it should reject", "source: ["},
		{"When a field is duplicated, it should reject", "source: one\nsource: two"},
		{"When a field is unknown, it should reject", "unknown: true"},
		{"When documents are repeated, it should reject", "source: one\n---\nsource: two"},
		{"When input is empty, it should reject", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var config manifest
			if err := decode([]byte(testCase.input), &config); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRestoreEnum(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		schema    apiextensionsv1.JSONSchemaProps
		path      []string
		wantError bool
	}{
		{"When the schema is a string enum, it should restore s1122", apiextensionsv1.JSONSchemaProps{Type: "string", Enum: []apiextensionsv1.JSON{{Raw: []byte(`"s922"`)}}}, nil, false},
		{"When the enum is unknown, it should reject", apiextensionsv1.JSONSchemaProps{Type: "string", Enum: []apiextensionsv1.JSON{{Raw: []byte(`"unknown"`)}}}, nil, true},
		{"When the enum is missing, it should reject", apiextensionsv1.JSONSchemaProps{Type: "string"}, nil, true},
		{"When the enum type changes, it should reject", apiextensionsv1.JSONSchemaProps{Type: "object"}, nil, true},
		{"When the path is missing, it should reject", apiextensionsv1.JSONSchemaProps{Type: "object"}, []string{"missing"}, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			values := []apiextensionsv1.JSON{{Raw: []byte(`"s922"`)}, {Raw: []byte(`"s1122"`)}}
			err := restoreEnum(&testCase.schema, testCase.path, values)
			if (err != nil) != testCase.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if !testCase.wantError && !reflect.DeepEqual(testCase.schema.Enum, values) {
				t.Fatal("enum not restored")
			}
		})
	}
}

func TestValidateSchema(t *testing.T) {
	files, err := filepath.Glob("testdata/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := decode(data, &crd); err != nil {
			t.Fatal(err)
		}
		for _, version := range crd.Spec.Versions {
			t.Run("When baseline shape is complete, it should accept", func(t *testing.T) {
				if err := validateSchema(version.Schema, crd.Name); err != nil {
					t.Fatalf("%s/%s: %v", crd.Name, version.Name, err)
				}
			})
			for _, testCase := range []struct {
				name   string
				mutate func(*apiextensionsv1.JSONSchemaProps)
			}{
				{"When spec has the wrong type, it should reject", func(schema *apiextensionsv1.JSONSchemaProps) {
					child := schema.Properties["spec"]
					child.Type = "string"
					schema.Properties["spec"] = child
				}},
				{"When spec is empty, it should reject", func(schema *apiextensionsv1.JSONSchemaProps) {
					child := schema.Properties["spec"]
					child.Properties = nil
					schema.Properties["spec"] = child
				}},
				{"When the root type changes, it should reject", func(schema *apiextensionsv1.JSONSchemaProps) { schema.Type = "string" }},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					invalid := version.Schema.DeepCopy()
					testCase.mutate(invalid.OpenAPIV3Schema)
					if err := validateSchema(invalid, crd.Name); err == nil {
						t.Fatalf("expected rejection: %s/%s", crd.Name, version.Name)
					}
				})
			}
			if !strings.Contains(crd.Name, "clustertemplates.") {
				t.Run("When status is missing, it should reject", func(t *testing.T) {
					invalid := version.Schema.DeepCopy()
					delete(invalid.OpenAPIV3Schema.Properties, "status")
					if err := validateSchema(invalid, crd.Name); err == nil {
						t.Fatalf("expected rejection: %s/%s", crd.Name, version.Name)
					}
				})
			}
			if strings.Contains(crd.Name, "templates.") {
				t.Run("When the nested template spec is missing, it should reject", func(t *testing.T) {
					invalid := version.Schema.DeepCopy()
					spec := invalid.OpenAPIV3Schema.Properties["spec"]
					template := spec.Properties["template"]
					delete(template.Properties, "spec")
					if err := validateSchema(invalid, crd.Name); err == nil {
						t.Fatalf("expected rejection: %s/%s", crd.Name, version.Name)
					}
				})
			}
			for _, testCase := range []struct {
				name  string
				field string
			}{
				{"When the apiVersion schema is missing, it should reject", "apiVersion"},
				{"When the kind schema is missing, it should reject", "kind"},
				{"When the metadata schema is missing, it should reject", "metadata"},
				{"When the spec schema is missing, it should reject", "spec"},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					invalid := version.Schema.DeepCopy()
					delete(invalid.OpenAPIV3Schema.Properties, testCase.field)
					if err := validateSchema(invalid, crd.Name); err == nil {
						t.Fatalf("expected rejection: %s/%s/%s", crd.Name, version.Name, testCase.field)
					}
				})
			}
		}
	}
}

func TestStagedGeneration(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "compat")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	for _, testCase := range []struct {
		name          string
		compatibility bool
		malformed     bool
		kindMutation  string
	}{
		{"When IBM staging succeeds, it should publish compatible CRDs", true, false, ""},
		{"When compatibility fails, it should leave the destination untouched", true, true, ""},
		{"When another provider generates, it should bypass compatibility unchanged", false, false, ""},
		{"When the resource kind is missing, it should preserve the destination", true, false, "missing"},
		{"When the resource kind is empty, it should preserve the destination", true, false, "empty"},
		{"When the resource kind is incorrect, it should preserve the destination", true, false, "wrong"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := fixtures(t, true)
			if testCase.malformed {
				if err := os.WriteFile(filepath.Join(input, "invalid.yaml"), []byte("invalid"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.kindMutation != "" {
				file := filepath.Join(input, "infrastructure.cluster.x-k8s.io_ibmpowervsclusters.yaml")
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				original := []byte("    kind: IBMPowerVSCluster\n")
				if !bytes.Contains(data, original) {
					t.Fatal("fixture is missing the expected kind")
				}
				replacement := map[string]string{"missing": "", "empty": "    kind: \"\"\n", "wrong": "    kind: IBMVPCCluster\n"}[testCase.kindMutation]
				if err := os.WriteFile(file, bytes.Replace(data, original, []byte(replacement), 1), 0644); err != nil {
					t.Fatal(err)
				}
			}
			destination := fixtures(t, false)
			if err := os.WriteFile(filepath.Join(destination, "sentinel.yaml"), []byte("unchanged"), 0644); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, destination)
			args := []string{filepath.Join(root, "hack/capi-crd-gen.sh"), "--no-workspace"}
			if testCase.compatibility {
				args = append(args, "--compat-tool", binary, "--compat-config", filepath.Join(root, "hack/capi-compat/ibmcloud/compatibility.yaml"))
			}
			args = append(args, destination, "bash", "-c", `cp "$1"/*.yaml "${2#output:crd:artifacts:config=}/"`, "generator", input)
			output, err := exec.Command("bash", args...).CombinedOutput()
			if testCase.malformed || testCase.kindMutation != "" {
				exitError, ok := err.(*exec.ExitError)
				if !ok || exitError.ExitCode() <= 0 {
					t.Fatalf("expected nonzero generation exit: %v: %s", err, output)
				}
				if testCase.kindMutation != "" && !bytes.Contains(output, []byte("unexpected CRD identity")) {
					t.Fatalf("expected identity rejection: %s", output)
				}
				if !reflect.DeepEqual(before, snapshot(t, destination)) {
					t.Fatal("destination changed")
				}
				return
			}
			if err != nil {
				t.Fatalf("generation: %v: %s", err, output)
			}
			if !testCase.compatibility && !reflect.DeepEqual(snapshot(t, input), snapshot(t, destination)) {
				t.Fatal("other provider output changed")
			}
			if testCase.compatibility {
				first := snapshot(t, destination)
				if err := run(configPath, destination); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(first, snapshot(t, destination)) {
					t.Fatal("output was not transformed")
				}
			}
		})
	}
}
