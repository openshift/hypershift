package e2eutilallowlist

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestRun(t *testing.T) {
	// Keep fixture allowances separate from the production migration baseline.
	original := allowlist
	allowlist = map[string]map[string][]string{
		"test/e2e/v2/good/good_test.go": {
			"TestAllowedSymbols":                {"GetConfig", "Version414", "Version51", "Predicate"},
			"Allowed.Method":                    {"GetConfig"},
			`Describe("existing registration")`: {"GetConfig"},
		},
		"test/e2e/v2/bad/bad_test.go": {
			"TestExistingCaller": {"GetConfig"},
			"Allowed.Method":     {"GetConfig"},
		},
	}
	t.Cleanup(func() { allowlist = original })
	for _, tt := range []struct{ name, pattern string }{
		{"When references match their enclosing function allowances, it should allow nested closures and methods", "test/e2e/v2/good"},
		{"When references are new, it should reject symbols in new callers and new symbols in existing callers", "test/e2e/v2/bad"},
		{"When legacy utilities are used outside v2, it should leave them unrestricted", "test/e2e/v1"},
	} {
		t.Run(tt.name, func(t *testing.T) { analysistest.Run(t, analysistest.TestData(), Analyzer, tt.pattern) })
	}
}

func TestIsAllowed(t *testing.T) {
	const file = "test/e2e/v2/internal/test_context.go"
	for _, tt := range []struct {
		name, file, function, symbol string
		want                         bool
	}{
		{"When an existing function references an existing symbol, it should allow it", file, "SetupTestContextFromEnv", "GetConfig", true},
		{"When an existing symbol is used in another file, it should reject it", "test/e2e/v2/new.go", "SetupTestContextFromEnv", "GetConfig", false},
		{"When an existing symbol is used in another function, it should reject it", file, "NewCaller", "GetConfig", false},
		{"When a new symbol is used in an existing function, it should reject it", file, "SetupTestContextFromEnv", "NewUtility", false},
		{"When a future version constant is used, it should reject it", file, "SetupTestContextFromEnv", "VersionFuture", false},
		{"When a symbol comes from another utility subpackage, it should reject it", file, "SetupTestContextFromEnv", "dump.GetConfig", false},
		{"When a reference has no enclosing function, it should reject it", file, "", "GetConfig", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAllowed(tt.file, tt.function, tt.symbol); got != tt.want {
				t.Errorf("isAllowed(%q, %q, %q) = %v, want %v", tt.file, tt.function, tt.symbol, got, tt.want)
			}
		})
	}
}
