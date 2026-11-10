package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectTypes(t *testing.T) {
	for _, tc := range []struct{ name, source, wantError string }{
		{name: "When API types contain recursive references, it should terminate and return a sorted closure", source: `package v1
type PodMonitoring struct { Ref *PodMonitoring }
type PodMonitoringList struct { Items []PodMonitoring }
type Rules struct { Ref *PodMonitoring }
type RulesList struct { Items []Rules }
type Unrelated string`},
		{name: "When a field name matches an unrelated type, it should not add that type to the closure", source: `package v1
type PodMonitoring struct { Authorization string }
type PodMonitoringList struct { Items []PodMonitoring }
type Rules struct{}
type RulesList struct { Items []Rules }
type Authorization interface { Reconcile() }`},
		{name: "When a required root type is missing, it should fail explicitly", source: "package v1", wantError: "missing upstream type PodMonitoring"},
		{name: "When a selected type uses a grouped declaration, it should require explicit generator support", source: `package v1
type (PodMonitoring struct{}; PodMonitoringList struct{})
type Rules struct{}
type RulesList struct{}`, wantError: "unsupported grouped upstream type declaration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "types.go", tc.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			types := map[string]*ast.GenDecl{}
			for _, node := range file.Decls {
				decl := node.(*ast.GenDecl)
				for _, spec := range decl.Specs {
					types[spec.(*ast.TypeSpec).Name.Name] = decl
				}
			}
			names, err := selectTypes(types)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(names, ",") != "PodMonitoring,PodMonitoringList,Rules,RulesList" {
				t.Fatalf("unexpected closure %v", names)
			}
		})
	}
}

func TestCollectImports(t *testing.T) {
	for _, tc := range []struct {
		name, path, wantError string
	}{
		{name: "When a selected declaration uses Kubernetes metadata, it should collect only that import", path: "k8s.io/apimachinery/pkg/apis/meta/v1"},
		{name: "When a selected declaration imports operator code, it should reject the dependency", path: "coupled/operator", wantError: "unsupported API import"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "types.go", "package v1\ntype Snapshot struct { Metadata meta.ObjectMeta }", 0)
			if err != nil {
				t.Fatal(err)
			}
			used := map[string]string{}
			err = collectImports(file.Decls[0].(*ast.GenDecl), map[string]string{"meta": tc.path, "unused": "coupled/unused"}, used)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(used) != 1 || used["meta"] != tc.path {
				t.Fatalf("unexpected imports: %v", used)
			}
		})
	}
}

func TestApiSourceFiles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing bool
	}{
		{name: "When the source directory contains mixed inputs, it should select sorted non-generated Go sources"},
		{name: "When the upstream API directory is missing, it should report the error", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := t.TempDir()
			directory := filepath.Join(upstream, "pkg/operator/apis/monitoring/v1")
			if !tc.missing {
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"z.go", "a.go", "a_test.go", "zz_generated.deepcopy.go", "README.md"} {
					if err := os.WriteFile(filepath.Join(directory, name), nil, 0644); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Mkdir(filepath.Join(directory, "nested.go"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			files, err := apiSourceFiles(upstream)
			if tc.missing {
				if !os.IsNotExist(err) {
					t.Fatalf("expected a missing directory error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "pkg/operator/apis/monitoring/v1/a.go,pkg/operator/apis/monitoring/v1/z.go"
			if strings.Join(files, ",") != want {
				t.Fatalf("unexpected source selection: %v", files)
			}
		})
	}
}

func TestGenerate(t *testing.T) {
	for _, tc := range []struct {
		name, nested, nestedImports, podHeader, wantError string
		extraFiles                                        map[string]string
		omitCondition                                     bool
	}{
		{name: "When nested data moves into another source file, it should copy only the data closure deterministically", nested: "struct { Labels map[string]string; Values []string }"},
		{name: "When API data depends on a runtime interface, it should reject the dependency", nested: "interface { Reconcile() }", wantError: "unexpectedly depends on interface"},
		{name: "When API data contains an anonymous runtime interface, it should reject the dependency", nested: "struct { Runtime interface { Reconcile() } }", wantError: "unexpectedly depends on interface"},
		{name: "When a selected type imports runtime code, it should reject the dependency", nested: "struct { Client operator.Client }", nestedImports: `import "coupled/operator"`, wantError: `unsupported API import "coupled/operator"`},
		{name: "When an unrelated file reuses an import alias, it should retain the selected type's import", nested: "struct { Label model.LabelName }", nestedImports: `import "github.com/prometheus/common/model"`, extraFiles: map[string]string{"unrelated.go": "package v1\nimport model \"coupled/operator\"\ntype Ignored struct { Client model.Client }"}},
		{name: "When selected declarations conflict on an import alias, it should report the conflict", nested: "struct { Ref shared.SecretReference }", nestedImports: `import shared "k8s.io/api/core/v1"`, extraFiles: map[string]string{"rules_types.go": "package v1\nimport shared \"k8s.io/apimachinery/pkg/apis/meta/v1\"\ntype Rules struct { Selector shared.LabelSelector }\ntype RulesList struct { Items []Rules }"}, wantError: "conflicting upstream import alias shared"},
		{name: "When the upstream condition constant is removed, it should report the missing declaration", nested: "string", omitCondition: true, wantError: "missing upstream monitoring condition constant"},
		{name: "When a source file belongs to another package, it should fail explicitly", nested: "string", extraFiles: map[string]string{"other.go": "package other"}, wantError: "unexpected upstream package"},
		{name: "When a selected type's documentation contains a go:generate directive, it should reject the directive", nested: "string", extraFiles: map[string]string{"transport_types.go": "package v1\n// Nested data.\n//go:generate touch generated\ntype Nested string"}, wantError: `contains Go directive "//go:generate touch generated"`},
		{name: "When a selected type's field comment contains a go:generate directive, it should reject the directive", nested: "struct {\n\t//go:generate touch generated\n\tValue string\n}", wantError: "contains Go directive"},
		{name: "When the upstream license header contains a build constraint, it should reject the directive", nested: "string", podHeader: "//go:build ignore\n\n", wantError: "license header"},
		{name: "When a type is declared in more than one upstream file, it should report the conflict instead of silently keeping the last one", nested: "string", extraFiles: map[string]string{"duplicate_types.go": "package v1\ntype PodMonitoring struct{}"}, wantError: `type "PodMonitoring" declared in both`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream, output := t.TempDir(), t.TempDir()
			input := filepath.Join(upstream, "pkg/operator/apis/monitoring/v1")
			if err := os.MkdirAll(input, 0755); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				"pod_types.go": tc.podHeader + `// Copyright Google LLC
package v1
import "coupled/operator"
// PodMonitoring carries monitoring data.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PodMonitoring struct { Spec *Nested }
type PodMonitoringList struct { Items []PodMonitoring }
type ClusterPodMonitoring struct { Runtime operator.Client }
func (*PodMonitoring) Runtime() { operator.Reconcile() }`,
				"transport_types.go": "package v1\n" + tc.nestedImports + "\ntype Nested " + tc.nested,
				"rules_types.go": `package v1
type Rules struct { State MonitoringConditionType }
type RulesList struct { Items []Rules }
type ClusterRules struct { Runtime string }`,
				"http_types.go": `package v1
type Unrelated struct { Runtime string }`,
				"monitoring_types.go": `package v1
type MonitoringConditionType string
const ( // Monitoring configuration condition.
ConfigurationCreateSuccess MonitoringConditionType = "ConfigurationCreateSuccess"
)`,
				"ignored_test.go":          "not valid Go source",
				"zz_generated.deepcopy.go": "not valid Go source",
			}
			if tc.omitCondition {
				files["monitoring_types.go"] = "package v1\ntype MonitoringConditionType string"
			}
			for name, source := range tc.extraFiles {
				files[name] = source
			}
			for name, source := range files {
				if err := os.WriteFile(filepath.Join(input, name), []byte(source), 0644); err != nil {
					t.Fatal(err)
				}
			}
			err := generate(upstream, output)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			first, err := os.ReadFile(filepath.Join(output, "types.go"))
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{"Copyright Google LLC", "type Nested", "type PodMonitoringList", "type RulesList", "ConfigurationCreateSuccess", "+k8s:deepcopy-gen"} {
				if !strings.Contains(string(first), required) {
					t.Fatalf("missing %q", required)
				}
			}
			for _, forbidden := range []string{"coupled/operator", "func (", "ClusterPodMonitoring", "ClusterRules", "Unrelated"} {
				if strings.Contains(string(first), forbidden) {
					t.Fatalf("unexpected runtime or unrelated declaration %q", forbidden)
				}
			}
			if err := generate(upstream, output); err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile(filepath.Join(output, "types.go"))
			if err != nil {
				t.Fatal(err)
			}
			if string(first) != string(second) {
				t.Fatal("generation changed on identical inputs")
			}
		})
	}
}

func TestRejectDirectives(t *testing.T) {
	for _, tc := range []struct{ name, source, wantError string }{
		{name: "When source contains only documentation and API markers, it should accept it", source: "// PodMonitoring carries data.\n// +kubebuilder:object:root=true\n// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object\ntype PodMonitoring struct{}"},
		{name: "When source contains a go:generate directive, it should reject it", source: "//go:generate touch generated", wantError: "//go:generate"},
		{name: "When source contains an indented go:generate directive, it should reject it", source: "type T struct {\n\t//go:generate touch generated\n}", wantError: "//go:generate"},
		{name: "When source contains a go:build constraint, it should reject it", source: "//go:build ignore", wantError: "//go:build"},
		{name: "When source contains a legacy build constraint, it should reject it", source: "// +build ignore", wantError: "// +build"},
		{name: "When source contains a line directive, it should reject it", source: "//line generated.go:1", wantError: "//line"},
		{name: "When source contains a cgo export directive, it should reject it", source: "//export Generated", wantError: "//export"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectDirectives([]byte(tc.source), "types.go")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
