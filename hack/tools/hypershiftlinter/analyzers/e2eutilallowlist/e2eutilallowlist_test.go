package e2eutilallowlist

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRun(t *testing.T) {
	t.Parallel()
	// Keep fixture allowances separate from the production migration baseline.
	allowed := map[string]map[string][]string{
		"test/e2e/v2/good/good_test.go": {
			"TestAllowedSymbols":                  {"GetConfig", "Version414", "Version51", "Predicate"},
			"Allowed.Method":                      {"GetConfig"},
			`Describe("existing registration")#1`: {"GetConfig"},
		},
		"test/e2e/v2/stale/stale.go": {
			"Existing": {"GetConfig", "Version414"},
			"Removed":  {"GetConfig"},
		},
		"test/e2e/v2/registrations/registrations.go": {
			`Describe("existing suite")#1/BeforeEach()#1`: {"Version414"},
			`Describe("duplicate")#1`:                     {"Version414"},
		},
		"test/e2e/v2/members/members.go": {
			"Existing": {"dump.Diagnostics", "dump.Diagnostics.Collect", "dump.Diagnostics.Message"},
		},
		"test/e2e/v2/bad/bad_test.go": {
			"TestExistingCaller": {"GetConfig"},
			"Allowed.Method":     {"GetConfig"},
		},
	}
	analyzer := newAnalyzer(allowed)
	for _, tt := range []struct{ name, pattern string }{
		{
			name:    "When references match their enclosing function allowances, it should allow nested closures and methods",
			pattern: "test/e2e/v2/good",
		},
		{
			name:    "When references are new, it should reject symbols in new callers and new symbols in existing callers",
			pattern: "test/e2e/v2/bad",
		},
		{
			name:    "When a spec or duplicate registration is new, it should not inherit an existing scope allowance",
			pattern: "test/e2e/v2/registrations",
		},
		{
			name:    "When types share member names, it should allow only the existing declaring type",
			pattern: "test/e2e/v2/members",
		},
		{
			name:    "When a function or reference is removed, it should report its unused allowance",
			pattern: "test/e2e/v2/stale",
		},
		{
			name:    "When legacy utilities are used outside v2, it should leave them unrestricted",
			pattern: "test/e2e/v1",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			analysistest.Run(t, analysistest.TestData(), analyzer, tt.pattern)
		})
	}
}

func TestIsAllowed(t *testing.T) {
	t.Parallel()
	const file = "test/e2e/v2/internal/test_context.go"
	for _, tt := range []struct {
		name, file, function, symbol string
		want                         bool
	}{
		{
			name:     "When an existing function references an existing symbol, it should allow it",
			file:     file,
			function: "SetupTestContextFromEnv",
			symbol:   "GetConfig",
			want:     true,
		},
		{
			name:     "When an existing symbol is used in another file, it should reject it",
			file:     "test/e2e/v2/new.go",
			function: "SetupTestContextFromEnv",
			symbol:   "GetConfig",
			want:     false,
		},
		{
			name:     "When an existing symbol is used in another function, it should reject it",
			file:     file,
			function: "NewCaller",
			symbol:   "GetConfig",
			want:     false,
		},
		{
			name:     "When a new symbol is used in an existing function, it should reject it",
			file:     file,
			function: "SetupTestContextFromEnv",
			symbol:   "NewUtility",
			want:     false,
		},
		{
			name:     "When a future version constant is used, it should reject it",
			file:     file,
			function: "SetupTestContextFromEnv",
			symbol:   "VersionFuture",
			want:     false,
		},
		{
			name:     "When a symbol comes from another utility subpackage, it should reject it",
			file:     file,
			function: "SetupTestContextFromEnv",
			symbol:   "dump.GetConfig",
			want:     false,
		},
		{
			name:     "When a reference has no enclosing function, it should reject it",
			file:     file,
			function: "",
			symbol:   "GetConfig",
			want:     false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAllowed(allowlist, tt.file, tt.function, tt.symbol); got != tt.want {
				t.Errorf("isAllowed(%q, %q, %q) = %v, want %v", tt.file, tt.function, tt.symbol, got, tt.want)
			}
		})
	}
}

func TestIsLegacyUtilPkg(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		path string
		want bool
	}{
		{name: "When the legacy utility package is imported, it should match", path: utilPkgPath, want: true},
		{name: "When a legacy utility subpackage is imported, it should match", path: utilPkgPath + "/dump", want: true},
		{name: "When a nested legacy subpackage is imported, it should match", path: utilPkgPath + "/dump/nested", want: true},
		{name: "When a sibling package shares the prefix, it should not match", path: utilPkgPath + "other"},
		{name: "When the v2 utility package is imported, it should not match", path: "github.com/openshift/hypershift/test/e2e/v2/util"},
		{name: "When the package path is empty, it should not match"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLegacyUtilPkg(tt.path); got != tt.want {
				t.Errorf("isLegacyUtilPkg(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestAllowlist(t *testing.T) {
	t.Parallel()
	for file := range allowlist {
		t.Run("When "+file+" has allowances, it should exist", func(t *testing.T) {
			info, err := os.Stat(filepath.Join("../../../../..", file))
			if err != nil {
				t.Fatalf("remove allowances for missing file %s: %v", file, err)
			}
			if info.IsDir() {
				t.Fatalf("allowlist key %s is a directory", file)
			}
		})
	}
}

func TestAllowlistOSImageStreamUpgradeScopes(t *testing.T) {
	t.Parallel()
	const filename = "test/e2e/v2/tests/nodepool_osimagestream_test.go"
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../../../../..", filename), nil, 0)
	if err != nil {
		t.Fatalf("parse OSImageStream upgrade specs: %v", err)
	}
	wantSymbols := []string{"EventuallyObject", "OSImageStreamPredicate", "Predicate", "UpdateObject", "WaitForReadyNodesByNodePool", "WithInterval", "WithTimeout"}
	for _, testCase := range []struct {
		name     string
		function string
	}{
		{name: "When the cross-major upgrade spec is renamed, it should retain only its existing utility allowances", function: "NodePoolOSImageStreamCrossMajorUpgradeTest"},
		{name: "When the pinned RHEL9 upgrade spec is renamed, it should retain only its existing utility allowances", function: "NodePoolOSImageStreamPinnedRHEL9UpgradeTest"},
		{name: "When the upgrade spec uses the v2 version waiter, it should remove obsolete condition allowances", function: "NodePoolOSImageStreamUpgradeVerificationTest"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var scopes []string
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Name.Name != testCase.function {
					continue
				}
				ast.Inspect(function.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					identifier, ok := call.Fun.(*ast.Ident)
					if !ok || identifier.Name != "It" {
						return true
					}
					info := &types.Info{Uses: map[*ast.Ident]types.Object{
						identifier: types.NewFunc(token.NoPos, types.NewPackage("github.com/onsi/ginkgo/v2", "ginkgo"), "It", nil),
					}}
					scopes = append(scopes, registrationScope(&analysis.Pass{TypesInfo: info}, call, testCase.function, map[string]int{}))
					return false
				})
			}
			if len(scopes) != 1 {
				t.Fatalf("%s has %d It registrations, want exactly one", testCase.function, len(scopes))
			}
			if got := allowlist[filename][scopes[0]]; !slices.Equal(got, wantSymbols) {
				t.Errorf("allowances for current callback %s = %v, want %v", scopes[0], got, wantSymbols)
			}
			for scope := range allowlist[filename] {
				if strings.HasPrefix(scope, testCase.function+"/") && scope != scopes[0] {
					t.Errorf("obsolete callback allowance %s remains alongside current callback %s", scope, scopes[0])
				}
			}
		})
	}
}

func TestMemberName(t *testing.T) {
	t.Parallel()
	pkg := types.NewPackage(utilPkgPath, "util")
	condition := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Condition", nil), types.NewStruct([]*types.Var{types.NewField(token.NoPos, pkg, "Status", types.Typ[types.String], false)}, nil), nil)
	embedded := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Wrapper", nil), types.NewStruct([]*types.Var{types.NewField(token.NoPos, pkg, "Condition", condition, true)}, nil), nil)
	alias := types.NewAlias(types.NewTypeName(token.NoPos, pkg, "Alias", nil), condition)
	for _, tt := range []struct {
		name     string
		receiver types.Type
		indices  []int
		want     string
	}{
		{name: "When a field belongs to a named type, it should include that type", receiver: condition, indices: []int{0}, want: "Condition.Status"},
		{name: "When a receiver is a pointer, it should use the named element", receiver: types.NewPointer(condition), want: "Condition.Status"},
		{name: "When a receiver is an alias, it should use the original type", receiver: alias, want: "Condition.Status"},
		{name: "When a field is promoted, it should use the declaring type", receiver: embedded, indices: []int{0, 0}, want: "Condition.Status"},
		{name: "When a type is anonymous, it should retain its structural identity", receiver: types.NewStruct(nil, nil), want: "struct{}.Status"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := memberName(tt.receiver, tt.indices, "Status"); got != tt.want {
				t.Errorf("memberName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegistrationScope(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		expression  string
		function    string
		packagePath string
		parent      string
		want        string
	}{
		{name: "When a top-level suite has a literal label, it should get its own scope", expression: `Describe("suite", nil)`, function: "Describe", packagePath: "github.com/onsi/ginkgo/v2", want: `Describe("suite")#1`},
		{name: "When a spec is registered through an import alias, it should get a child scope", expression: `g.It("spec", nil)`, function: "It", packagePath: "github.com/onsi/ginkgo/v2", parent: "Existing", want: `Existing/It("spec")#1`},
		{name: "When a setup hook has no label, it should get its own scope", expression: `BeforeEach(nil)`, function: "BeforeEach", packagePath: "github.com/onsi/ginkgo/v2", parent: "Existing", want: `Existing/BeforeEach()#1`},
		{name: "When a label is computed, it should remain distinct from literal labels", expression: `It(label, nil)`, function: "It", packagePath: "github.com/onsi/ginkgo/v2", want: `It(<dynamic>)#1`},
		{name: "When an unrelated function has a Ginkgo name, it should inherit its parent scope", expression: `It("spec", nil)`, function: "It", packagePath: "test/e2e/v2/custom", parent: "Existing", want: "Existing"},
		{name: "When a Ginkgo function does not register a callback, it should inherit its parent scope", expression: `Expect(nil)`, function: "Expect", packagePath: "github.com/onsi/ginkgo/v2", parent: "Existing", want: "Existing"},
		{name: "When the callee is a function literal, it should inherit its parent scope", expression: `(func(){})()`, parent: "Existing", want: "Existing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			expression, err := parser.ParseExpr(tt.expression)
			if err != nil {
				t.Fatal(err)
			}
			call := expression.(*ast.CallExpr)
			info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
			var identifier *ast.Ident
			switch callee := call.Fun.(type) {
			case *ast.Ident:
				identifier = callee
			case *ast.SelectorExpr:
				identifier = callee.Sel
			}
			if identifier != nil {
				info.Uses[identifier] = types.NewFunc(token.NoPos, types.NewPackage(tt.packagePath, "ginkgo"), tt.function, nil)
			}
			pass := &analysis.Pass{TypesInfo: info}
			occurrences := map[string]int{}
			if got := registrationScope(pass, call, tt.parent, occurrences); got != tt.want {
				t.Errorf("registrationScope() = %q, want %q", got, tt.want)
			}
			// Duplicate labels and unlabeled hooks must not share an allowance.
			if strings.HasSuffix(tt.want, "#1") {
				want := strings.TrimSuffix(tt.want, "#1") + "#2"
				if got := registrationScope(pass, call, tt.parent, occurrences); got != want {
					t.Errorf("duplicate scope = %q, want %q", got, want)
				}
			}
		})
	}
}
