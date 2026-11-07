package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/yaml"

	yamlv3 "go.yaml.in/yaml/v3"
)

type manifest struct {
	Source string `json:"source"`
	CRDs   []rule `json:"crds"`
}

type rule struct {
	Name     string                                            `json:"name"`
	Versions []apiextensionsv1.CustomResourceDefinitionVersion `json:"versions"`
	Enums    []enumRule                                        `json:"enums,omitempty"`
}

type enumRule struct {
	Path   []string               `json:"path"`
	Values []apiextensionsv1.JSON `json:"values"`
}

var inventory = map[string]struct {
	kind     string
	versions int
}{
	"ibmpowervsclusters":         {"IBMPowerVSCluster", 2},
	"ibmpowervsclustertemplates": {"IBMPowerVSClusterTemplate", 2},
	"ibmpowervsimages":           {"IBMPowerVSImage", 2},
	"ibmpowervsmachines":         {"IBMPowerVSMachine", 2},
	"ibmpowervsmachinetemplates": {"IBMPowerVSMachineTemplate", 2},
	"ibmvpcclusters":             {"IBMVPCCluster", 2},
	"ibmvpcclustertemplates":     {"IBMVPCClusterTemplate", 1},
	"ibmvpcmachines":             {"IBMVPCMachine", 2},
	"ibmvpcmachinetemplates":     {"IBMVPCMachineTemplate", 2},
}

func main() {
	config := flag.String("config", "", "Maintained compatibility manifest")
	dir := flag.String("dir", "", "Staged CRD directory")
	flag.Parse()
	if err := run(*config, *dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(config, dir string) error {
	data, err := os.ReadFile(config)
	if err != nil {
		return err
	}
	var compatibility manifest
	if err := decode(data, &compatibility); err != nil {
		return err
	}
	if err := compatibility.validate(); err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return err
	}
	if len(files) != len(compatibility.CRDs) {
		return fmt.Errorf("expected nine CRDs, got %d", len(files))
	}
	outputs := map[string][]byte{}
	seen := sets.New[string]()
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := decode(data, &crd); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if seen.Has(crd.Name) {
			return fmt.Errorf("duplicate CRD %s", crd.Name)
		}
		seen.Insert(crd.Name)
		var selected *rule
		for index := range compatibility.CRDs {
			if compatibility.CRDs[index].Name == crd.Name {
				selected = &compatibility.CRDs[index]
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("unknown CRD %s", crd.Name)
		}
		if err := selected.apply(&crd); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		encoded, err := yaml.Marshal(struct {
			metav1.TypeMeta `json:",inline"`
			Metadata        metav1.ObjectMeta                            `json:"metadata"`
			Spec            apiextensionsv1.CustomResourceDefinitionSpec `json:"spec"`
		}{crd.TypeMeta, crd.ObjectMeta, crd.Spec})
		if err != nil {
			return err
		}
		outputs[file] = encoded
	}
	for _, file := range files {
		if err := os.WriteFile(file, outputs[file], 0644); err != nil {
			return err
		}
	}
	return nil
}

func decode(data []byte, target any) error {
	decoder := yamlv3.NewDecoder(bytes.NewReader(data))
	var document yamlv3.Node
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	if err := decoder.Decode(&document); err != io.EOF {
		return fmt.Errorf("expected exactly one YAML document")
	}
	return yaml.UnmarshalStrict(data, target)
}

func (config *manifest) validate() error {
	if config.Source == "" || len(config.CRDs) != len(inventory) {
		return fmt.Errorf("manifest requires provenance and nine CRDs")
	}
	seen := sets.New[string]()
	for _, entry := range config.CRDs {
		plural := bytes.TrimSuffix([]byte(entry.Name), []byte(".infrastructure.cluster.x-k8s.io"))
		resource, known := inventory[string(plural)]
		count := resource.versions
		if !known || entry.Name != string(plural)+".infrastructure.cluster.x-k8s.io" || seen.Has(entry.Name) || len(entry.Versions) != count {
			return fmt.Errorf("invalid manifest CRD %s", entry.Name)
		}
		seen.Insert(entry.Name)
		for index, version := range entry.Versions {
			expected := "v1beta2"
			if count == 2 && index == 0 {
				expected = "v1beta1"
			}
			if version.Name != expected || !version.Served || version.Storage != (expected == "v1beta2") || version.Deprecated || version.DeprecationWarning != nil {
				return fmt.Errorf("invalid version policy for %s", entry.Name)
			}
			legacy := bytes.HasPrefix(plural, []byte("ibmpowervs")) && expected == "v1beta1"
			if legacy != (version.Schema != nil) || (legacy && validateSchema(version.Schema, entry.Name) != nil) {
				return fmt.Errorf("invalid legacy schema for %s", entry.Name)
			}
		}
		expectedPath := []string(nil)
		switch string(plural) {
		case "ibmpowervsmachines":
			expectedPath = []string{"spec", "systemType"}
		case "ibmpowervsmachinetemplates":
			expectedPath = []string{"spec", "template", "spec", "systemType"}
		}
		if expectedPath == nil && len(entry.Enums) != 0 {
			return fmt.Errorf("unexpected enum policy for %s", entry.Name)
		}
		if expectedPath != nil {
			if len(entry.Enums) != 1 || !reflect.DeepEqual(entry.Enums[0].Path, expectedPath) {
				return fmt.Errorf("missing enum policy for %s", entry.Name)
			}
			var values []string
			for _, value := range entry.Enums[0].Values {
				var text string
				if err := json.Unmarshal(value.Raw, &text); err != nil {
					return err
				}
				values = append(values, text)
			}
			sort.Strings(values)
			if !reflect.DeepEqual(values, []string{"", "e1050", "e1080", "e980", "s1022", "s1122", "s922"}) {
				return fmt.Errorf("invalid enum values for %s: %v", entry.Name, values)
			}
		}
	}
	return nil
}

func (entry *rule) apply(crd *apiextensionsv1.CustomResourceDefinition) error {
	resource, known := inventory[crd.Spec.Names.Plural]
	if !known || crd.Name != entry.Name || crd.APIVersion != "apiextensions.k8s.io/v1" || crd.Kind != "CustomResourceDefinition" || crd.Spec.Group != "infrastructure.cluster.x-k8s.io" || crd.Spec.Scope != apiextensionsv1.NamespaceScoped || crd.Spec.Names.Plural+"."+crd.Spec.Group != entry.Name || crd.Spec.Names.Kind != resource.kind {
		return fmt.Errorf("unexpected CRD identity")
	}
	versions := map[string]apiextensionsv1.CustomResourceDefinitionVersion{}
	for _, version := range crd.Spec.Versions {
		if _, duplicate := versions[version.Name]; duplicate {
			return fmt.Errorf("duplicate version %s", version.Name)
		}
		known := false
		for _, policy := range entry.Versions {
			if policy.Name == version.Name {
				known = true
			}
		}
		if !known || validateSchema(version.Schema, entry.Name) != nil {
			return fmt.Errorf("unexpected version or schema %s", version.Name)
		}
		versions[version.Name] = version
	}
	var result []apiextensionsv1.CustomResourceDefinitionVersion
	for _, policy := range entry.Versions {
		version, found := versions[policy.Name]
		if policy.Schema != nil {
			if found && !reflect.DeepEqual(version, policy) {
				return fmt.Errorf("conflicting legacy version %s", policy.Name)
			}
			version = *policy.DeepCopy()
		} else if !found {
			return fmt.Errorf("missing version %s", policy.Name)
		}
		version.Served, version.Storage = policy.Served, policy.Storage
		version.Deprecated, version.DeprecationWarning = policy.Deprecated, policy.DeprecationWarning
		if version.Name == "v1beta2" {
			for _, enum := range entry.Enums {
				if err := restoreEnum(version.Schema.OpenAPIV3Schema, enum.Path, enum.Values); err != nil {
					return err
				}
			}
		}
		result = append(result, version)
	}
	crd.Spec.Versions = result
	return nil
}

func validateSchema(validation *apiextensionsv1.CustomResourceValidation, name string) error {
	if validation == nil || validation.OpenAPIV3Schema == nil || validation.OpenAPIV3Schema.Type != "object" {
		return fmt.Errorf("missing object schema")
	}
	schema := validation.OpenAPIV3Schema
	fields := map[string]string{"apiVersion": "string", "kind": "string", "metadata": "object", "spec": "object"}
	if !strings.Contains(name, "clustertemplates.") {
		fields["status"] = "object"
	}
	for field, expectedType := range fields {
		child, found := schema.Properties[field]
		if !found || child.Type != expectedType || (field == "spec" && len(child.Properties) == 0) {
			return fmt.Errorf("missing or malformed %s schema", field)
		}
	}
	if strings.Contains(name, "templates.") {
		template := schema.Properties["spec"].Properties["template"]
		spec := template.Properties["spec"]
		if template.Type != "object" || spec.Type != "object" || len(spec.Properties) == 0 {
			return fmt.Errorf("missing template spec schema")
		}
	}
	return nil
}

func restoreEnum(schema *apiextensionsv1.JSONSchemaProps, path []string, values []apiextensionsv1.JSON) error {
	if len(path) == 0 {
		if schema.Type != "string" || len(schema.Enum) == 0 {
			return fmt.Errorf("unexpected systemType schema")
		}
		for _, actual := range schema.Enum {
			known := false
			for _, expected := range values {
				if bytes.Equal(actual.Raw, expected.Raw) {
					known = true
				}
			}
			if !known {
				return fmt.Errorf("unknown systemType value %s", actual.Raw)
			}
		}
		schema.Enum = append([]apiextensionsv1.JSON(nil), values...)
		return nil
	}
	child, found := schema.Properties[path[0]]
	if !found || schema.Type != "object" {
		return fmt.Errorf("missing enum path %v", path)
	}
	if err := restoreEnum(&child, path[1:], values); err != nil {
		return err
	}
	schema.Properties[path[0]] = child
	return nil
}
