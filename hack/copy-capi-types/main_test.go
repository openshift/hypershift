package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestIsAllowedImport(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "When given a standard library import, it should be allowed", path: "fmt", want: true},
		{name: "When given a multi-part stdlib import, it should be allowed", path: "net/http", want: true},
		{name: "When given k8s.io/apimachinery, it should be allowed", path: "k8s.io/apimachinery/pkg/apis/meta/v1", want: true},
		{name: "When given k8s.io/api, it should be allowed", path: "k8s.io/api/core/v1", want: true},
		{name: "When given k8s.io/utils, it should be allowed", path: "k8s.io/utils/ptr", want: true},
		{name: "When given controller-runtime scheme, it should be allowed", path: "sigs.k8s.io/controller-runtime/pkg/scheme", want: true},
		{name: "When given cluster-api api, it should be allowed", path: "sigs.k8s.io/cluster-api/api/v1beta1", want: true},
		{name: "When given cluster-api errors, it should be allowed", path: "sigs.k8s.io/cluster-api/errors", want: true},
		{name: "When given a banned third-party import, it should not be allowed", path: "github.com/pkg/errors", want: false},
		{name: "When given controller-runtime outside scheme, it should not be allowed", path: "sigs.k8s.io/controller-runtime/pkg/client", want: false},
		{name: "When given a CAPI provider import, it should not be allowed", path: "sigs.k8s.io/cluster-api-provider-aws/v2/pkg/cloud", want: false},
		{name: "When given k8s.io/client-go, it should not be allowed", path: "k8s.io/client-go/kubernetes", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllowedImport(tt.path)
			if got != tt.want {
				t.Errorf("isAllowedImport(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestImportAlias(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantName string
	}{
		{
			name:     "When given a simple import path, it should return the last segment",
			src:      `"github.com/pkg/errors"`,
			wantName: "errors",
		},
		{
			name:     "When given a versioned module path, it should return the second-to-last segment",
			src:      `"sigs.k8s.io/cluster-api-provider-aws/v2"`,
			wantName: "cluster-api-provider-aws",
		},
		{
			name:     "When given an explicit alias, it should return the alias",
			src:      `aliasname "github.com/some/package"`,
			wantName: "aliasname",
		},
		{
			name:     "When given a single-segment path, it should return it directly",
			src:      `"fmt"`,
			wantName: "fmt",
		},
		{
			name:     "When given a v1beta1 path, it should return v1beta1 as the package name",
			src:      `"sigs.k8s.io/cluster-api/api/v1beta1"`,
			wantName: "v1beta1",
		},
		{
			name:     "When given a v1alpha1 path, it should return v1alpha1 as the package name",
			src:      `"sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha1"`,
			wantName: "v1alpha1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "", "package p\nimport "+tt.src, 0)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			got := importAlias(file.Imports[0])
			if got != tt.wantName {
				t.Errorf("importAlias() = %q, want %q", got, tt.wantName)
			}
		})
	}
}

func TestFindBannedImports(t *testing.T) {
	src := `package p

import (
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)
`
	file, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	t.Run("When given a file with mixed imports, it should return only banned ones", func(t *testing.T) {
		banned := findBannedImports(file)
		var paths []string
		for _, imp := range banned {
			paths = append(paths, strings.Trim(imp.Path.Value, `"`))
		}
		want := []string{
			"github.com/pkg/errors",
			"sigs.k8s.io/controller-runtime/pkg/client",
		}
		if diff := cmp.Diff(want, paths); diff != "" {
			t.Errorf("findBannedImports() mismatch (-want +got):\n%s", diff)
		}
	})

	// Verify allowed imports are NOT in the banned list
	t.Run("When given a file with stdlib and k8s imports, it should not ban them", func(t *testing.T) {
		banned := findBannedImports(file)
		for _, imp := range banned {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "fmt" || strings.HasPrefix(path, "k8s.io/apimachinery/") {
				t.Errorf("unexpected banned import: %s", path)
			}
		}
	})
}

func TestReferencesAny(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		symbols map[string]bool
		want    bool
	}{
		{
			name: "When a function calls a symbol, it should return true",
			src: `package p
func Foo() { Bar() }`,
			symbols: map[string]bool{"Bar": true},
			want:    true,
		},
		{
			name: "When a function does not call the symbol, it should return false",
			src: `package p
func Foo() { Baz() }`,
			symbols: map[string]bool{"Bar": true},
			want:    false,
		},
		{
			name: "When a function has no body, it should return false",
			src: `package p
type I interface { Foo() }`,
			symbols: map[string]bool{"Bar": true},
			want:    false,
		},
		{
			name: "When a function uses a symbol in a composite literal, it should return true",
			src: `package p
func Foo() { _ = MyType{} }`,
			symbols: map[string]bool{"MyType": true},
			want:    true,
		},
		{
			name: "When a function uses a symbol in a type assertion, it should return true",
			src: `package p
func Foo(x interface{}) { _ = x.(MyType) }`,
			symbols: map[string]bool{"MyType": true},
			want:    true,
		},
		{
			name: "When a function uses a symbol in a variable declaration, it should return true",
			src: `package p
func Foo() { var x MyType; _ = x }`,
			symbols: map[string]bool{"MyType": true},
			want:    true,
		},
		{
			name: "When a function has a stripped type in its parameter list, it should return true",
			src: `package p
func Foo(x MyType) {}`,
			symbols: map[string]bool{"MyType": true},
			want:    true,
		},
		{
			name: "When a function has a stripped type in its return list, it should return true",
			src: `package p
func Foo() MyType { return MyType{} }`,
			symbols: map[string]bool{"MyType": true},
			want:    true,
		},
		{
			name: "When a method receiver uses a stripped type, it should return true",
			src: `package p
type Other struct{}
func (o *Other) Foo() StrippedType { return StrippedType{} }`,
			symbols: map[string]bool{"StrippedType": true},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "", tt.src, 0)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			var fn *ast.FuncDecl
			for _, decl := range file.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok {
					fn = fd
					break
				}
			}
			if fn == nil {
				// For the interface test, there's no FuncDecl with a body
				// Use the first FuncDecl from the interface
				for _, decl := range file.Decls {
					if gd, ok := decl.(*ast.GenDecl); ok {
						for _, spec := range gd.Specs {
							if ts, ok := spec.(*ast.TypeSpec); ok {
								if iface, ok := ts.Type.(*ast.InterfaceType); ok {
									for _, m := range iface.Methods.List {
										fn = &ast.FuncDecl{Name: m.Names[0], Type: m.Type.(*ast.FuncType)}
										break
									}
								}
							}
						}
					}
				}
			}
			if fn == nil {
				t.Fatal("no func decl found")
			}
			got := nodeReferencesAny(fn, tt.symbols)
			if got != tt.want {
				t.Errorf("nodeReferencesAny() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeReferencesAny_GenDecl(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		symbols map[string]bool
		want    bool
	}{
		{
			name: "When a type embeds a stripped symbol, it should return true",
			src: `package p
type Foo struct { Bar }`,
			symbols: map[string]bool{"Bar": true},
			want:    true,
		},
		{
			name: "When a var references a stripped symbol, it should return true",
			src: `package p
var x = Foo{}`,
			symbols: map[string]bool{"Foo": true},
			want:    true,
		},
		{
			name: "When a type has no references to stripped symbols, it should return false",
			src: `package p
type Foo struct { Name string }`,
			symbols: map[string]bool{"Bar": true},
			want:    false,
		},
		{
			name: "When a const references a stripped symbol, it should return true",
			src: `package p
const x = Foo`,
			symbols: map[string]bool{"Foo": true},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "", tt.src, 0)
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			var gd *ast.GenDecl
			for _, decl := range file.Decls {
				if d, ok := decl.(*ast.GenDecl); ok && d.Tok != token.IMPORT {
					gd = d
					break
				}
			}
			if gd == nil {
				t.Fatal("no GenDecl found")
			}
			got := nodeReferencesAny(gd, tt.symbols)
			if got != tt.want {
				t.Errorf("nodeReferencesAny() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCollectStrippedSymbols(t *testing.T) {
	src := `package p

func Foo() {}
func (b *Bar) Method() {}
type Baz struct{}
var qux = 1
`
	file, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	funcSet := make(map[*ast.FuncDecl]bool)
	genSet := make(map[*ast.GenDecl]bool)
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			funcSet[d] = true
		case *ast.GenDecl:
			if d.Tok != token.IMPORT {
				genSet[d] = true
			}
		}
	}

	t.Run("When given funcs with receivers and GenDecls, it should collect all symbol forms", func(t *testing.T) {
		symbols := collectStrippedSymbols(funcSet, genSet)

		wantSymbols := []string{"Foo", "Method", "Bar.Method", "Baz", "qux"}
		for _, s := range wantSymbols {
			if !symbols[s] {
				t.Errorf("expected symbol %q to be in stripped set", s)
			}
		}
	})
}

func TestProcessFile_NoBannedImports(t *testing.T) {
	t.Run("When a file has no banned imports, it should return actionCopy with unchanged content", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MyType struct {
	metav1.ObjectMeta
}

func Foo() {
	fmt.Println("hello")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, stripped, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}
		if r.action != actionCopy {
			t.Errorf("got action %d, want actionCopy (%d)", r.action, actionCopy)
		}
		if len(stripped) != 0 {
			t.Errorf("expected no stripped symbols, got %v", stripped)
		}
		if diff := cmp.Diff(src, string(r.content)); diff != "" {
			t.Errorf("content mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestProcessFile_StripBannedFunctions(t *testing.T) {
	t.Run("When a file has functions using banned imports, it should strip them and keep clean ones", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	"github.com/pkg/errors"
)

type MyType struct {
	Name string
}

func Good() {
	fmt.Println("hello")
}

func Bad() error {
	return errors.New("oops")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, stripped, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}
		if r.action != actionStrip {
			t.Errorf("got action %d, want actionStrip (%d)", r.action, actionStrip)
		}
		if !stripped["Bad"] {
			t.Errorf("expected 'Bad' in stripped symbols, got %v", stripped)
		}

		output := string(r.content)
		if !strings.Contains(output, "func Good()") {
			t.Errorf("expected Good() to be preserved in output")
		}
		if strings.Contains(output, "func Bad()") {
			t.Errorf("expected Bad() to be stripped from output")
		}
		if strings.Contains(output, "pkg/errors") {
			t.Errorf("expected banned import to be removed from output")
		}
	})
}

func TestProcessFile_StripBannedTypes(t *testing.T) {
	t.Run("When a file has types referencing banned imports, it should strip them", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GoodType struct {
	Name string
}

type BadType struct {
	Client client.Client
}

func Hello() {
	fmt.Println("hi")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, stripped, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}
		if r.action != actionStrip {
			t.Errorf("got action %d, want actionStrip (%d)", r.action, actionStrip)
		}
		if !stripped["BadType"] {
			t.Errorf("expected 'BadType' in stripped symbols, got %v", stripped)
		}

		output := string(r.content)
		if !strings.Contains(output, "GoodType") {
			t.Errorf("expected GoodType to be preserved")
		}
		if strings.Contains(output, "BadType") {
			t.Errorf("expected BadType to be stripped")
		}
	})
}

func TestProcessFile_WithinFileCascade(t *testing.T) {
	t.Run("When func A calls func B that uses a banned import, both should be stripped", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	"github.com/pkg/errors"
)

type MyType struct {
	Name string
}

func Good() {
	fmt.Println("hello")
}

func Bad() error {
	return errors.New("oops")
}

func CallsBad() {
	Bad()
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, stripped, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}
		if r.action != actionStrip {
			t.Errorf("got action %d, want actionStrip (%d)", r.action, actionStrip)
		}
		if !stripped["Bad"] || !stripped["CallsBad"] {
			t.Errorf("expected both Bad and CallsBad in stripped symbols, got %v", stripped)
		}

		output := string(r.content)
		if !strings.Contains(output, "func Good()") {
			t.Errorf("expected Good() to be preserved")
		}
		if strings.Contains(output, "func Bad()") {
			t.Errorf("expected Bad() to be stripped")
		}
		if strings.Contains(output, "func CallsBad()") {
			t.Errorf("expected CallsBad() to be stripped")
		}
	})
}

func TestProcessFile_FullFileSkip(t *testing.T) {
	t.Run("When all declarations use banned imports, it should skip the entire file", func(t *testing.T) {
		src := `package p

import "github.com/pkg/errors"

func OnlyBad() error {
	return errors.New("oops")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, stripped, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}
		if r.action != actionSkip {
			t.Errorf("got action %d, want actionSkip (%d)", r.action, actionSkip)
		}
		if !stripped["OnlyBad"] {
			t.Errorf("expected 'OnlyBad' in stripped symbols, got %v", stripped)
		}
	})
}

func TestProcessFile_ImportCleanup(t *testing.T) {
	t.Run("When stripping removes all uses of an import, that import should be removed", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	"github.com/pkg/errors"
)

func Good() {
	fmt.Println("hello")
}

func Bad() error {
	return errors.New("oops")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, _, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}

		output := string(r.content)
		if strings.Contains(output, "pkg/errors") {
			t.Errorf("expected unused import to be removed, but found it in output:\n%s", output)
		}
		if !strings.Contains(output, `"fmt"`) {
			t.Errorf("expected used import 'fmt' to be preserved")
		}
	})

	t.Run("When a blank import is present, it should be preserved even if no code references it", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	_ "k8s.io/api/core/v1"
)

func Hello() {
	fmt.Println("hello")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, _, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}

		output := string(r.content)
		if !strings.Contains(output, `_ "k8s.io/api/core/v1"`) {
			t.Errorf("expected blank import to be preserved, but it was removed:\n%s", output)
		}
	})
}

func TestProcessFile_SkipByName(t *testing.T) {
	tests := []struct {
		name     string
		filename string
	}{
		{name: "When given zz_generated.defaults.go, it should be skipped by main()", filename: "zz_generated.defaults.go"},
		{name: "When given zz_generated.conversion.go, it should be skipped by main()", filename: "zz_generated.conversion.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := skipFiles[tt.filename]; !ok {
				t.Errorf("expected %q to be in skipFiles map", tt.filename)
			}
		})
	}
}

func TestProcessFile_SkipBySuffix(t *testing.T) {
	t.Run("When given a webhook file, it should be skipped by suffix", func(t *testing.T) {
		if _, ok := skipSuffixes["_webhook.go"]; !ok {
			t.Errorf("expected '_webhook.go' to be in skipSuffixes map")
		}
	})
}

func TestCrossFileCascade_FieldNameCollision(t *testing.T) {
	t.Run("When a struct field has the same name as a stripped function, the struct should NOT be stripped", func(t *testing.T) {
		srcA := `package p

import "github.com/pkg/errors"

func Name() error {
	return errors.New("bad")
}
`
		srcB := `package p

type MyType struct {
	Name string
}
`
		dir := t.TempDir()
		pathA := filepath.Join(dir, "a.go")
		pathB := filepath.Join(dir, "b.go")
		if err := os.WriteFile(pathA, []byte(srcA), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathB, []byte(srcB), 0644); err != nil {
			t.Fatal(err)
		}

		rA, strippedA, err := processFile(pathA)
		if err != nil {
			t.Fatalf("processFile(a.go) error: %v", err)
		}
		rB, strippedB, err := processFile(pathB)
		if err != nil {
			t.Fatalf("processFile(b.go) error: %v", err)
		}

		allStripped := make(map[string]bool)
		for sym := range strippedA {
			allStripped[sym] = true
		}
		for sym := range strippedB {
			allStripped[sym] = true
		}

		results := []fileResult{
			{base: "a.go", result: rA},
			{base: "b.go", result: rB},
		}

		crossFileCascade(results, allStripped)

		if results[1].result.action == actionSkip {
			t.Errorf("expected file b.go to NOT be skipped — struct field 'Name' should not trigger cascade from stripped function 'Name'")
		}
		output := string(results[1].result.content)
		if !strings.Contains(output, "MyType") {
			t.Errorf("expected MyType to be preserved despite field name matching stripped function name:\n%s", output)
		}
	})
}

func TestCrossFileCascade_FuncDecl(t *testing.T) {
	t.Run("When file B calls a function stripped from file A, it should strip it in Pass 2", func(t *testing.T) {
		srcA := `package p

import "github.com/pkg/errors"

func StrippedFunc() error {
	return errors.New("bad")
}
`
		srcB := `package p

import "fmt"

type MyType struct {
	Name string
}

func KeepMe() {
	fmt.Println("hello")
}

func CallsStripped() {
	StrippedFunc()
}
`
		dir := t.TempDir()
		pathA := filepath.Join(dir, "a.go")
		pathB := filepath.Join(dir, "b.go")
		if err := os.WriteFile(pathA, []byte(srcA), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathB, []byte(srcB), 0644); err != nil {
			t.Fatal(err)
		}

		rA, strippedA, err := processFile(pathA)
		if err != nil {
			t.Fatalf("processFile(a.go) error: %v", err)
		}
		rB, strippedB, err := processFile(pathB)
		if err != nil {
			t.Fatalf("processFile(b.go) error: %v", err)
		}

		allStripped := make(map[string]bool)
		for sym := range strippedA {
			allStripped[sym] = true
		}
		for sym := range strippedB {
			allStripped[sym] = true
		}

		results := []fileResult{
			{base: "a.go", result: rA},
			{base: "b.go", result: rB},
		}

		crossFileCascade(results, allStripped)

		output := string(results[1].result.content)
		if strings.Contains(output, "CallsStripped") {
			t.Errorf("expected CallsStripped() to be stripped by cross-file cascade, but found it in output:\n%s", output)
		}
		if !strings.Contains(output, "KeepMe") {
			t.Errorf("expected KeepMe() to be preserved in output")
		}
		if !strings.Contains(output, "MyType") {
			t.Errorf("expected MyType to be preserved in output")
		}
	})
}

func TestCrossFileCascade_GenDecl(t *testing.T) {
	t.Run("When file B has a type referencing a type stripped from file A, it should strip it in Pass 2", func(t *testing.T) {
		srcA := `package p

import "github.com/pkg/errors"

type BadType struct {
	Err errors.Error
}
`
		srcB := `package p

type KeepType struct {
	Name string
}

type RefsBadType struct {
	Bad BadType
}
`
		dir := t.TempDir()
		pathA := filepath.Join(dir, "a.go")
		pathB := filepath.Join(dir, "b.go")
		if err := os.WriteFile(pathA, []byte(srcA), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathB, []byte(srcB), 0644); err != nil {
			t.Fatal(err)
		}

		rA, strippedA, err := processFile(pathA)
		if err != nil {
			t.Fatalf("processFile(a.go) error: %v", err)
		}
		rB, strippedB, err := processFile(pathB)
		if err != nil {
			t.Fatalf("processFile(b.go) error: %v", err)
		}

		allStripped := make(map[string]bool)
		for sym := range strippedA {
			allStripped[sym] = true
		}
		for sym := range strippedB {
			allStripped[sym] = true
		}

		results := []fileResult{
			{base: "a.go", result: rA},
			{base: "b.go", result: rB},
		}

		crossFileCascade(results, allStripped)

		output := string(results[1].result.content)
		if strings.Contains(output, "RefsBadType") {
			t.Errorf("expected RefsBadType to be stripped by cross-file cascade, but found it in output:\n%s", output)
		}
		if !strings.Contains(output, "KeepType") {
			t.Errorf("expected KeepType to be preserved in output")
		}
	})
}

func TestCrossFileCascade_FullFileSkip(t *testing.T) {
	t.Run("When all remaining decls in a file reference stripped symbols, the file should be fully skipped", func(t *testing.T) {
		srcA := `package p

import "github.com/pkg/errors"

func StrippedFunc() error {
	return errors.New("bad")
}
`
		srcB := `package p

func OnlyCallsStripped() {
	StrippedFunc()
}
`
		dir := t.TempDir()
		pathA := filepath.Join(dir, "a.go")
		pathB := filepath.Join(dir, "b.go")
		if err := os.WriteFile(pathA, []byte(srcA), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathB, []byte(srcB), 0644); err != nil {
			t.Fatal(err)
		}

		rA, strippedA, err := processFile(pathA)
		if err != nil {
			t.Fatalf("processFile(a.go) error: %v", err)
		}
		rB, strippedB, err := processFile(pathB)
		if err != nil {
			t.Fatalf("processFile(b.go) error: %v", err)
		}

		allStripped := make(map[string]bool)
		for sym := range strippedA {
			allStripped[sym] = true
		}
		for sym := range strippedB {
			allStripped[sym] = true
		}

		results := []fileResult{
			{base: "a.go", result: rA},
			{base: "b.go", result: rB},
		}

		crossFileCascade(results, allStripped)

		if results[1].result.action != actionSkip {
			t.Errorf("expected file b.go to be actionSkip after cross-file cascade, got action %d", results[1].result.action)
		}
	})
}

func TestCrossFileCascade_NoStrippedSymbols(t *testing.T) {
	t.Run("When there are no stripped symbols, it should not modify results", func(t *testing.T) {
		src := `package p

func Hello() {}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "a.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, _, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}

		results := []fileResult{{base: "a.go", result: r}}
		crossFileCascade(results, map[string]bool{})

		if results[0].result.action != actionCopy {
			t.Errorf("expected action to remain actionCopy, got %d", results[0].result.action)
		}
	})
}

func TestCrossFileCascade_TransitiveCascade(t *testing.T) {
	t.Run("When func C calls func B which calls stripped func A, all should cascade", func(t *testing.T) {
		srcA := `package p

import "github.com/pkg/errors"

func StrippedA() error {
	return errors.New("bad")
}
`
		srcB := `package p

import "fmt"

func KeepMe() {
	fmt.Println("safe")
}

func CallsA() {
	StrippedA()
}

func CallsCallsA() {
	CallsA()
}
`
		dir := t.TempDir()
		pathA := filepath.Join(dir, "a.go")
		pathB := filepath.Join(dir, "b.go")
		if err := os.WriteFile(pathA, []byte(srcA), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pathB, []byte(srcB), 0644); err != nil {
			t.Fatal(err)
		}

		rA, strippedA, err := processFile(pathA)
		if err != nil {
			t.Fatalf("processFile(a.go) error: %v", err)
		}
		rB, strippedB, err := processFile(pathB)
		if err != nil {
			t.Fatalf("processFile(b.go) error: %v", err)
		}

		allStripped := make(map[string]bool)
		for sym := range strippedA {
			allStripped[sym] = true
		}
		for sym := range strippedB {
			allStripped[sym] = true
		}

		results := []fileResult{
			{base: "a.go", result: rA},
			{base: "b.go", result: rB},
		}

		crossFileCascade(results, allStripped)

		output := string(results[1].result.content)
		if strings.Contains(output, "CallsA") {
			t.Errorf("expected CallsA() and CallsCallsA() to be stripped, but found in output:\n%s", output)
		}
		if !strings.Contains(output, "KeepMe") {
			t.Errorf("expected KeepMe() to be preserved")
		}
	})
}

func TestCrossFileCascade_UpdatesAllStrippedSymbols(t *testing.T) {
	t.Run("When file B's cascade discovers new symbols, file C should also be updated", func(t *testing.T) {
		// File A: has a banned import, StrippedA is removed in Pass 1.
		srcA := `package p

import "github.com/pkg/errors"

func StrippedA() error {
	return errors.New("bad")
}
`
		// File B: CallsA references StrippedA (cross-file cascade removes it).
		srcB := `package p

func CallsA() {
	StrippedA()
}

func KeepB() {}
`
		// File C: UsesCallsA references CallsA. This should only be stripped
		// if allStrippedSymbols is updated with "CallsA" after processing file B.
		srcC := `package p

func UsesCallsA() {
	CallsA()
}

func KeepC() {}
`
		dir := t.TempDir()
		for name, content := range map[string]string{
			"a.go": srcA, "b.go": srcB, "c.go": srcC,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}

		rA, strippedA, err := processFile(filepath.Join(dir, "a.go"))
		if err != nil {
			t.Fatalf("processFile(a.go) error: %v", err)
		}
		rB, strippedB, err := processFile(filepath.Join(dir, "b.go"))
		if err != nil {
			t.Fatalf("processFile(b.go) error: %v", err)
		}
		rC, strippedC, err := processFile(filepath.Join(dir, "c.go"))
		if err != nil {
			t.Fatalf("processFile(c.go) error: %v", err)
		}

		allStripped := make(map[string]bool)
		for sym := range strippedA {
			allStripped[sym] = true
		}
		for sym := range strippedB {
			allStripped[sym] = true
		}
		for sym := range strippedC {
			allStripped[sym] = true
		}

		results := []fileResult{
			{base: "a.go", result: rA},
			{base: "b.go", result: rB},
			{base: "c.go", result: rC},
		}

		crossFileCascade(results, allStripped)

		outputC := string(results[2].result.content)
		if strings.Contains(outputC, "UsesCallsA") {
			t.Errorf("expected UsesCallsA() to be stripped via multi-file cascade, but found in output:\n%s", outputC)
		}
		if !strings.Contains(outputC, "KeepC") {
			t.Errorf("expected KeepC() to be preserved")
		}
	})
}

func TestProcessFile_BannedVarDecl(t *testing.T) {
	t.Run("When a var declaration references a banned import, it should be stripped", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	"github.com/pkg/errors"
)

var ErrBad = errors.New("bad")

func Good() {
	fmt.Println("hello")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, stripped, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}
		if r.action != actionStrip {
			t.Errorf("got action %d, want actionStrip (%d)", r.action, actionStrip)
		}
		if !stripped["ErrBad"] {
			t.Errorf("expected 'ErrBad' in stripped symbols, got %v", stripped)
		}

		output := string(r.content)
		if strings.Contains(output, "ErrBad") {
			t.Errorf("expected ErrBad to be stripped from output")
		}
		if !strings.Contains(output, "func Good()") {
			t.Errorf("expected Good() to be preserved")
		}
	})
}

func TestProcessFile_MethodReceiver(t *testing.T) {
	t.Run("When a method on a type uses a banned import, it should be stripped with type.method format", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	"github.com/pkg/errors"
)

type MyType struct {
	Name string
}

func (m *MyType) Good() {
	fmt.Println(m.Name)
}

func (m *MyType) Bad() error {
	return errors.New("bad")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, stripped, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}
		if r.action != actionStrip {
			t.Errorf("got action %d, want actionStrip (%d)", r.action, actionStrip)
		}
		if !stripped["Bad"] || !stripped["MyType.Bad"] {
			t.Errorf("expected both 'Bad' and 'MyType.Bad' in stripped symbols, got %v", stripped)
		}

		output := string(r.content)
		if strings.Contains(output, "func (m *MyType) Bad()") {
			t.Errorf("expected Bad method to be stripped from output")
		}
		if !strings.Contains(output, "func (m *MyType) Good()") {
			t.Errorf("expected Good method to be preserved")
		}
	})
}

func TestProcessFile_MultiSpecVarBlock(t *testing.T) {
	t.Run("When a var block has some specs using banned imports, it should strip the entire block", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	"github.com/pkg/errors"
)

var (
	ErrBad  = errors.New("bad")
	ErrGood = fmt.Errorf("good")
)

func Hello() {
	fmt.Println("hello")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, stripped, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}
		if r.action != actionStrip {
			t.Errorf("got action %d, want actionStrip (%d)", r.action, actionStrip)
		}
		if !stripped["ErrBad"] {
			t.Errorf("expected 'ErrBad' in stripped symbols, got %v", stripped)
		}

		output := string(r.content)
		if strings.Contains(output, "ErrBad") {
			t.Errorf("expected ErrBad to be stripped from output:\n%s", output)
		}
		if !strings.Contains(output, "func Hello()") {
			t.Errorf("expected Hello() to be preserved")
		}
	})
}

func TestProcessFile_DotImport(t *testing.T) {
	t.Run("When a dot import is present, it should be preserved even if no selector references it", func(t *testing.T) {
		src := `package p

import (
	"fmt"
	. "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MyResource struct {
	ObjectMeta
}

func Hello() {
	fmt.Println("hello")
}
`
		dir := t.TempDir()
		path := filepath.Join(dir, "types.go")
		if err := os.WriteFile(path, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}

		r, _, err := processFile(path)
		if err != nil {
			t.Fatalf("processFile() error: %v", err)
		}

		output := string(r.content)
		if !strings.Contains(output, `. "k8s.io/apimachinery/pkg/apis/meta/v1"`) {
			t.Errorf("expected dot import to be preserved, but it was removed:\n%s", output)
		}
	})
}
