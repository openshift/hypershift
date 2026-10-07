package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestVerifyAllowedAPIDependencies(t *testing.T) {
	tests := []struct {
		name           string
		goMod          string
		allowedModules sets.Set[string]
		wantError      string
	}{
		{
			name: "When direct requirements are allowed and indirect requirements are not allowlisted, it should return no error",
			goMod: `
module github.com/openshift/hypershift/api

go 1.26.0

require (
	github.com/openshift/api v0.0.0-20260922100147-3a6e03c5a473
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
)`,
			allowedModules: sets.New("github.com/openshift/api"),
		},
		{
			name: "When a direct requirement is not allowed, it should report the unauthorized dependency",
			goMod: `
module github.com/openshift/hypershift/api

go 1.26.0

require github.com/openshift/client-go v0.0.0-20261001003915-dcaad1dc7fe8
`,
			allowedModules: sets.New[string](),
			wantError:      "github.com/openshift/client-go",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := verifyAllowedAPIDependencies(loadTestGoMod(t, filepath.Join(t.TempDir(), "api"), test.goMod).file, test.allowedModules)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q, got nil", test.wantError)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected error containing %q, got %q", test.wantError, err)
			}
		})
	}
}

func TestVerifySharedDependencies(t *testing.T) {
	tests := []struct {
		name                     string
		rootGoMod                string
		apiGoMod                 string
		wantError                string
		wantErrorContains        []string
		wantErrorContainsInOrder []string
	}{
		{
			name: "When shared direct and indirect versions match, it should return no error",
			rootGoMod: goModWith(`
require (
	example.com/shared v1.2.3
	example.com/indirect v1.0.0 // indirect
)`),
			apiGoMod: apiGoModWith(`
require (
	example.com/shared v1.2.3
	example.com/indirect v1.0.0 // indirect
)`),
		},
		{
			name:      "When directness differs but the shared version matches, it should return no error",
			rootGoMod: goModWith("require example.com/shared v1.2.3"),
			apiGoMod:  apiGoModWith("require example.com/shared v1.2.3 // indirect"),
		},
		{
			name: "When dependencies and replacements are unique to one module, it should ignore them",
			rootGoMod: goModWith(`
require (
	example.com/shared v1.2.3
	example.com/root-only v1.0.0
)
replace example.com/root-only => ./root-only
replace github.com/openshift/hypershift/api => ./api
replace sigs.k8s.io/karpenter => example.com/karpenter v1.0.0`),
			apiGoMod: apiGoModWith(`
require (
	example.com/shared v1.2.3
	example.com/api-only v1.0.0
)
replace example.com/api-only => ./api-only`),
		},
		{
			name: "When replace-only paths are not literal shared requirements, it should ignore them",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace example.com/transitive v1.0.0 => example.com/fork v1.0.1`),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.3
replace example.com/transitive v1.0.0 => example.com/other-fork v1.0.1`),
		},
		{
			name: "When matching unversioned module replacements exist, it should return no error",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace example.com/shared => example.com/fork v1.2.4`),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.3
replace example.com/shared => example.com/fork v1.2.4`),
		},
		{
			name: "When matching active and inactive version-scoped replacements exist, it should return no error",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace (
	example.com/shared v1.2.3 => example.com/fork v1.2.4
	example.com/shared v0.9.0 => example.com/fork v0.9.1
)`),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.3
replace (
	example.com/shared v1.2.3 => example.com/fork v1.2.4
	example.com/shared v0.9.0 => example.com/fork v0.9.1
)`),
		},
		{
			name: "When a root-only active replacement key exists, it should report the missing API key",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace example.com/shared v1.2.3 => example.com/fork v1.2.4`),
			apiGoMod: apiGoModWith("require example.com/shared v1.2.3"),
			wantError: replacementError(`  example.com/shared v1.2.3
    go.mod:     => example.com/fork v1.2.4
    api/go.mod: <missing>`),
		},
		{
			name: "When a root-only inactive replacement key exists, it should report the missing API key",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace example.com/shared v0.9.0 => example.com/fork v0.9.1`),
			apiGoMod: apiGoModWith("require example.com/shared v1.2.3"),
			wantError: replacementError(`  example.com/shared v0.9.0
    go.mod:     => example.com/fork v0.9.1
    api/go.mod: <missing>`),
		},
		{
			name:      "When an API-only active replacement key exists, it should report the missing root key",
			rootGoMod: goModWith("require example.com/shared v1.2.3"),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.3
replace example.com/shared v1.2.3 => example.com/fork v1.2.4`),
			wantError: replacementError(`  example.com/shared v1.2.3
    go.mod:     <missing>
    api/go.mod: => example.com/fork v1.2.4`),
		},
		{
			name:      "When an API-only inactive replacement key exists, it should report the missing root key",
			rootGoMod: goModWith("require example.com/shared v1.2.3"),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.3
replace example.com/shared v0.9.0 => example.com/fork v0.9.1`),
			wantError: replacementError(`  example.com/shared v0.9.0
    go.mod:     <missing>
    api/go.mod: => example.com/fork v0.9.1`),
		},
		{
			name: "When replacement target module paths differ, it should report both targets",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace example.com/shared => example.com/fork v1.2.4`),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.3
replace example.com/shared => example.com/other-fork v1.2.4`),
			wantError: replacementError(`  example.com/shared <all versions>
    go.mod:     => example.com/fork v1.2.4
    api/go.mod: => example.com/other-fork v1.2.4`),
		},
		{
			name: "When replacement target versions differ, it should report both targets",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace example.com/shared => example.com/fork v1.2.4`),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.3
replace example.com/shared => example.com/fork v1.2.5`),
			wantError: replacementError(`  example.com/shared <all versions>
    go.mod:     => example.com/fork v1.2.4
    api/go.mod: => example.com/fork v1.2.5`),
		},
		{
			name: "When one replacement target omits a module version, it should report local and module targets",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace example.com/shared => example.com/fork v1.2.4`),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.3
replace example.com/shared => ./example.com/fork`),
			wantErrorContains: []string{"example.com/shared <all versions>", "=> example.com/fork v1.2.4", "=> ./example.com/fork (resolved:"},
		},
		{
			name: "When unversioned and version-scoped keys have identical targets, it should report both missing exact keys",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace example.com/shared => example.com/fork v1.2.4`),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.3
replace example.com/shared v1.2.3 => example.com/fork v1.2.4`),
			wantErrorContains: []string{"example.com/shared <all versions>", "example.com/shared v1.2.3", "go.mod:     <missing>", "api/go.mod: <missing>"},
		},
		{
			name: "When multiple keys and paths mismatch, it should report deterministic path and version ordering",
			rootGoMod: goModWith(`
require (
	example.com/z v1.2.0
	example.com/a v1.2.0
)
replace (
	example.com/z v1.2.0 => example.com/zfork v1.2.1
	example.com/a v1.2.0 => example.com/afork v1.2.1
	example.com/a => example.com/afork v1.2.1
)`),
			apiGoMod: apiGoModWith(`
require (
	example.com/z v1.2.0
	example.com/a v1.2.0
)`),
			wantErrorContainsInOrder: []string{"example.com/a <all versions>", "example.com/a v1.2.0", "example.com/z v1.2.0"},
		},
		{
			name: "When requirement and replacement mismatches coexist, it should report both stable sections",
			rootGoMod: goModWith(`
require example.com/shared v1.2.3
replace example.com/shared => example.com/fork v1.2.4`),
			apiGoMod: apiGoModWith(`
require example.com/shared v1.2.2
replace example.com/shared => example.com/other-fork v1.2.4`),
			wantError: `shared dependency versions do not match:
  example.com/shared
    go.mod:     v1.2.3
    api/go.mod: v1.2.2

shared dependency replacements do not match:
  example.com/shared <all versions>
    go.mod:     => example.com/fork v1.2.4
    api/go.mod: => example.com/other-fork v1.2.4

align shared requirements and replacements and run ` + "`make update`",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			rootMod := loadTestGoMod(t, filepath.Join(base, "root"), test.rootGoMod)
			apiMod := loadTestGoMod(t, filepath.Join(base, "root", "api"), test.apiGoMod)
			err := verifySharedDependencies(rootMod, apiMod)
			assertError(t, err, test.wantError, test.wantErrorContains, test.wantErrorContainsInOrder)
		})
	}
}

func TestVerifySharedDependenciesLocalTargets(t *testing.T) {
	tests := []struct {
		name              string
		rootReplacement   string
		apiReplacement    string
		wantErrorContains []string
	}{
		{
			name:            "When module-relative local targets resolve to the same cleaned absolute path, it should return no error",
			rootReplacement: "./third_party/x",
			apiReplacement:  "../third_party/x",
		},
		{
			name:              "When identical local target literals resolve to different directories, it should report both resolutions",
			rootReplacement:   "./third_party/x",
			apiReplacement:    "./third_party/x",
			wantErrorContains: []string{"=> ./third_party/x (resolved:", filepath.Join("root", "third_party", "x"), filepath.Join("root", "api", "third_party", "x")},
		},
		{
			name:            "When clean and unclean local targets resolve to the same path, it should return no error",
			rootReplacement: "./third_party/../third_party/x",
			apiReplacement:  "../third_party/x",
		},
		{
			name:              "When a local target and module target differ, it should report both target forms",
			rootReplacement:   "./third_party/x",
			apiReplacement:    "example.com/fork v1.2.4",
			wantErrorContains: []string{"=> ./third_party/x (resolved:", "=> example.com/fork v1.2.4"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			rootMod := loadTestGoMod(t, filepath.Join(base, "root"), goModWith("require example.com/shared v1.2.3\nreplace example.com/shared => "+test.rootReplacement))
			apiMod := loadTestGoMod(t, filepath.Join(base, "root", "api"), apiGoModWith("require example.com/shared v1.2.3\nreplace example.com/shared => "+test.apiReplacement))
			err := verifySharedDependencies(rootMod, apiMod)
			assertError(t, err, "", test.wantErrorContains, nil)
		})
	}
}

func goModWith(body string) string {
	return "module github.com/openshift/hypershift\n\ngo 1.26.0\n\n" + body
}

func apiGoModWith(body string) string {
	return "module github.com/openshift/hypershift/api\n\ngo 1.26.0\n\n" + body
}

func replacementError(entries string) string {
	return "shared dependency replacements do not match:\n" + entries + "\n\nalign shared requirements and replacements and run `make update`"
}

func loadTestGoMod(t *testing.T, directory, content string) *goModFile {
	t.Helper()

	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("failed to create test module directory: %v", err)
	}
	path := filepath.Join(directory, "go.mod")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write test go.mod: %v", err)
	}

	goMod, err := loadGoMod(path)
	if err != nil {
		t.Fatalf("failed to load test go.mod: %v", err)
	}
	return goMod
}

func assertError(t *testing.T, err error, wantError string, contains, containsInOrder []string) {
	t.Helper()

	if wantError == "" && len(contains) == 0 && len(containsInOrder) == 0 {
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		return
	}
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if wantError != "" && err.Error() != wantError {
		t.Fatalf("expected error %q, got %q", wantError, err)
	}
	for _, expected := range contains {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("expected error containing %q, got %q", expected, err)
		}
	}
	position := 0
	for _, expected := range containsInOrder {
		relativePosition := strings.Index(err.Error()[position:], expected)
		if relativePosition < 0 {
			t.Fatalf("expected error containing %q after byte %d, got %q", expected, position, err)
		}
		position += relativePosition + len(expected)
	}
}
