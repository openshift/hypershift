package e2eutilallowlist

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRun(t *testing.T) {
	t.Parallel()
	// Keep fixture allowances separate from the production migration baseline.
	allowed := map[string]map[string][]string{
		"test/e2e/v2/good/good_test.go": {
			"TestAllowedSymbols":                {"GetConfig", "Version414", "Version51", "Predicate"},
			"Allowed.Method":                    {"GetConfig"},
			`Describe("existing registration")`: {"GetConfig"},
		},
		"test/e2e/v2/stale/stale.go": {
			"Existing": {"GetConfig", "Version414"},
			"Removed":  {"GetConfig"},
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
