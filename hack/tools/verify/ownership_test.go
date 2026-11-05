package verify

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOwnershipRules(t *testing.T) {
	policy := repositoryOwnership
	facts := ownershipFacts{
		paths:     []string{"main.go", "go.mod", "go.sum", "Makefile", "README.md", "api/go.mod", "api/go.sum", "api/v1/types.go", "api/v1/types_test.go", "api/v1/zz_generated.deepcopy.go", "api/vendor/modules.txt", "api/vendor/k8s.io/api/zz_generated.deepcopy.go", "vendor/modules.txt", "client/clientset.go", "client/informers.go", "cmd/install/install.go", "cmd/infra/client.go", "cmd/infra/create.go", "cmd/crds/doc.go", "cmd/crds/hostedclusters.yaml", "cmd/crds/tests/hc/stable.testsuite.yaml", "control-plane-operator/main.go", "hypershift-operator/main.go", "sidecar/main.go", "support/util.go", "brand-new-dir/main.go", "docs/index.md", "pkg/AGENTS.md", "test/e2e/e2e_test.go"},
		binaries:  []ownershipBinary{{"hypershift", "CLI", ""}, {"hypershift-operator", "HO", "hypershift-operator"}, {"control-plane-operator", "CPO", "control-plane-operator"}},
		importers: map[string][]string{"sidecar": {"CPO"}, "support": {"CPO", "HO"}, "cmd": {"CLI", "HO"}},
		generated: map[string]bool{"api/v1/zz_generated.deepcopy.go": true, "client/clientset.go": true, "client/informers.go": true, "cmd/infra/client.go": true, "cmd/crds/hostedclusters.yaml": true}, outputDirs: []string{"cmd/crds"},
	}
	rules := ownershipRules(policy, facts)
	for file, want := range map[string]string{"main.go": "CLI", "go.mod": "Dependencies", "api/go.mod": "API", "api/v1/types.go": "API", "api/v1/types_test.go": "E2E", "api/v1/zz_generated.deepcopy.go": "Vendor", "api/vendor/k8s.io/api/zz_generated.deepcopy.go": "Vendor", "vendor/modules.txt": "Vendor", "client/clientset.go": "Vendor", "cmd/infra/client.go": "Vendor", "cmd/infra/create.go": "CLI", "cmd/crds/doc.go": "CLI", "cmd/crds/hostedclusters.yaml": "Vendor", "cmd/crds/tests/hc/stable.testsuite.yaml": "E2E", "control-plane-operator/main.go": "CPO", "hypershift-operator/main.go": "HO", "sidecar/main.go": "CPO", "support/util.go": "HO", "brand-new-dir/main.go": "Tooling", "docs/index.md": "Docs", "README.md": "Docs", "pkg/AGENTS.md": "Docs", "test/e2e/e2e_test.go": "E2E", "Makefile": "Tooling"} {
		t.Run("When classifying "+file+", it should assign "+want, func(t *testing.T) {
			got := ""
			for _, rule := range rules {
				if matchPath(rule.Pattern, file) {
					got = rule.Group
					break
				}
			}
			if got != want {
				t.Fatalf("got %s, want %s", got, want)
			}
		})
	}
}
func TestParseBinaries(t *testing.T) {
	makefile := "build: new-operator\n\nnew-operator:\n\t$(GO) build -o bin/new-operator ./new-operator\n"
	if _, err := parseBinaries(makefile, map[string]string{}); err == nil {
		t.Fatal("new binary without group was accepted")
	}
	got, err := parseBinaries(makefile, map[string]string{"new-operator": "HO"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []ownershipBinary{{"new-operator", "HO", "new-operator"}}) {
		t.Fatalf("got %v", got)
	}
}
func TestOwnershipTable(t *testing.T) {
	input := "intro <!-- END GENERATED -->\n<!-- BEGIN GENERATED: old -->\nstale\n<!-- END GENERATED -->\noutro\n"
	got, err := ownershipTable(input, []ownershipGroup{{Name: "Docs", Prefix: "docs", Owns: "documentation"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "intro <!-- END GENERATED -->\n") || !strings.HasSuffix(got, "\noutro\n") || strings.Contains(got, "stale") {
		t.Fatalf("unexpected replacement: %s", got)
	}
	if _, err := ownershipTable("no markers", nil); err == nil {
		t.Fatal("missing markers were accepted")
	}
}
func TestRunner_Command(t *testing.T) {
	r := &runner{ctx: context.Background(), root: t.TempDir(), env: []string{"PWD=/old", "CGO_ENABLED=0"}}
	dir := filepath.Join(r.root, "symlinked-gopath")
	cmd := r.command(dir, []string{"GOFLAGS="}, "go", "version")
	if cmd.Dir != dir {
		t.Fatalf("got directory %s", cmd.Dir)
	}
	if !strings.Contains(strings.Join(cmd.Env, "\n"), "PWD="+dir) {
		t.Fatalf("PWD does not preserve symlink directory: %v", cmd.Env)
	}
}

func TestRunner_RootModuleFile(t *testing.T) {
	r := &runner{root: t.TempDir()}
	nested := filepath.Join(r.root, "hack/tools")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module tools\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		want       bool
	}{
		{"When a consumer belongs to root, it should include it", "support/capi/types.go", true},
		{"When a consumer belongs to tools, it should exclude it", "hack/tools/verify/providers.go", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.rootModuleFile(tc.path); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunner_StageCRDs(t *testing.T) {
	r := &runner{ctx: context.Background(), root: t.TempDir()}
	dir := filepath.Join(r.root, "cmd/install/assets/crds/cluster-api")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "existing.yaml")
	if err := os.WriteFile(original, []byte("existing manifest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := r.stageCRDs("cluster-api", false, []string{"crd"}); err == nil {
		t.Fatal("missing generator unexpectedly succeeded")
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "existing manifest\n" {
		t.Fatalf("generation failure changed existing manifests: %q, %v", data, err)
	}
}
