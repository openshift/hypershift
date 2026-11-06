package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveUnusedImports(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "test.go", `package test
import kept "kept"
import removed "removed"
func use() { kept.Call() }
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	removeUnusedImports(fileSet, file)
	if len(file.Imports) != 1 || file.Imports[0].Path.Value != `"kept"` {
		t.Fatalf("unexpected retained imports: %#v", file.Imports)
	}
}

func TestLocalize(t *testing.T) {
	source := []byte(`package v1beta1
import beta2 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta2"
func init() {}
func RegisterConversions() {}
func (source *FixedIP) ConvertTo() {}
func Convert(source *FixedIP) *beta2.FixedIP { return &beta2.FixedIP{FixedIP: source.FixedIP} }
`)
	actual, err := localize(source, map[string]bool{"FixedIP": true}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"package openstackconversion", "source *capov1beta1.FixedIP", "FixedIP: source.FixedIP", "Code generated"} {
		if !strings.Contains(string(actual), expected) {
			t.Errorf("missing %q in %s", expected, actual)
		}
	}
	for _, removed := range []string{"func init", "func RegisterConversions", "ConvertTo"} {
		if strings.Contains(string(actual), removed) {
			t.Errorf("retained %s", removed)
		}
	}
	if _, err := localize([]byte("invalid Go"), nil, false); err == nil {
		t.Fatal("accepted invalid source")
	}
}

func TestGenerate(t *testing.T) {
	source := t.TempDir()
	output := t.TempDir()
	for _, filename := range []string{"api/v1beta1/conversion.go", "api/v1beta1/zz_generated.conversion.go", "api/v1beta2/conversion_helpers.go"} {
		path := filepath.Join(source, filename)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package v1beta1\nfunc Convert() {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := generate(source, output); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, filename := range []string{"zz_generated.manual.go", "zz_generated.mapping.go", "zz_generated.conditions.go"} {
		data, err := os.ReadFile(filepath.Join(output, filename))
		if err != nil {
			t.Fatal(err)
		}
		before[filename] = data
	}
	if err := generate(source, output); err != nil {
		t.Fatal(err)
	}
	for filename, expected := range before {
		actual, err := os.ReadFile(filepath.Join(output, filename))
		if err != nil || !bytes.Equal(expected, actual) {
			t.Fatalf("non-deterministic %s: %v", filename, err)
		}
	}
	if err := generate(t.TempDir(), output); err == nil {
		t.Fatal("accepted missing conversion inputs")
	}
}
