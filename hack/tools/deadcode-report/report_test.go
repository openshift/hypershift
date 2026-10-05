package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	. "github.com/onsi/gomega"
)

func TestReadFindings(t *testing.T) {
	t.Parallel()
	t.Run("When the analyzer successfully emits null, it should report an empty array", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		findings, totals, err := readFindings([]byte("null"), t.TempDir(), "github.com/openshift/hypershift")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(findings).To(Equal([]finding{}))
		g.Expect(totals.candidates).To(BeZero())
	})
	for _, input := range []string{"", "{}", "[{}]", "[] []", "[", `[{"Name":"main","Path":"github.com/openshift/hypershift","Funcs":[{"Name":"Dead"}]}]`} {
		t.Run("When analyzer output is invalid "+input+", it should fail rather than report zero findings", func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			_, _, err := readFindings([]byte(input), t.TempDir(), "github.com/openshift/hypershift")
			g.Expect(err).To(HaveOccurred())
		})
	}
	t.Run("When findings contain excluded sources and duplicates, it should retain sorted first-party declarations", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		root := t.TempDir()
		for _, file := range []string{"support/helpers.go", "support/helpers_test.go", "support/generated.go", "support/thirdparty/copied.go", "vendor/github.com/openshift/hypershift/api/api.go", "hack/tools/helper.go", "hack/tools/go.mod"} {
			writeTestFile(t, root, file, "")
		}
		packages := []analyzerPackage{
			{Name: "support", Path: "github.com/openshift/hypershift/support", Funcs: []analyzerFunction{
				testFunction("Zebra", "support/helpers.go", false),
				testFunction("Alpha", "support/sub/../helpers.go", false),
				testFunction("Alpha", filepath.Join(root, "support/helpers.go"), false),
				testFunction("TestHelper", "support/helpers_test.go", false),
				testFunction("Generated", "support/generated.go", true),
				testFunction("Copied", "support/thirdparty/copied.go", false),
			}},
			{Name: "api", Path: "github.com/openshift/hypershift/api", Funcs: []analyzerFunction{testFunction("PublicAPI", "vendor/github.com/openshift/hypershift/api/api.go", false)}},
			{Name: "tools", Path: "github.com/openshift/hypershift/hack/tools", Funcs: []analyzerFunction{testFunction("OtherModule", "hack/tools/helper.go", false)}},
			{Name: "other", Path: "github.com/openshift/hypershift-other", Funcs: []analyzerFunction{testFunction("OtherPackage", "support/helpers.go", false)}},
			{Name: "support", Path: "github.com/openshift/hypershift/support", Funcs: []analyzerFunction{testFunction("Outside", filepath.Join(t.TempDir(), "outside.go"), false)}},
		}
		data, err := json.Marshal(packages)
		g.Expect(err).NotTo(HaveOccurred())
		findings, totals, err := readFindings(data, root, "github.com/openshift/hypershift")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(findings).To(Equal([]finding{
			{Package: "github.com/openshift/hypershift/support", Function: "Alpha", File: "support/helpers.go", Line: 4, Column: 6},
			{Package: "github.com/openshift/hypershift/support", Function: "TestHelper", File: "support/helpers_test.go", Line: 4, Column: 6},
			{Package: "github.com/openshift/hypershift/support", Function: "Zebra", File: "support/helpers.go", Line: 4, Column: 6},
		}))
		g.Expect(totals.candidates).To(Equal(10))
		g.Expect(totals.generated).To(Equal(1))
		g.Expect(totals.vendor).To(Equal(1))
		g.Expect(totals.copied).To(Equal(1))
		g.Expect(totals.external).To(Equal(3))
		g.Expect(totals.duplicates).To(Equal(1))
	})
	t.Run("When package and declaration order changes, it should produce identical sorted reports", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		root := t.TempDir()
		for _, file := range []string{"support/a.go", "support/z.go", "cmd/main.go"} {
			writeTestFile(t, root, file, "package fixture")
		}
		packages := []analyzerPackage{
			{Name: "support", Path: "github.com/openshift/hypershift/support", Funcs: []analyzerFunction{testFunction("Zebra", "support/z.go", false), testFunction("Alpha", "support/a.go", false)}},
			{Name: "cmd", Path: "github.com/openshift/hypershift/cmd", Funcs: []analyzerFunction{testFunction("Helper", "cmd/main.go", false)}},
		}
		artifacts := []string{t.TempDir(), t.TempDir()}
		for i, dir := range artifacts {
			data, err := json.Marshal(packages)
			g.Expect(err).NotTo(HaveOccurred())
			findings, totals, err := readFindings(data, root, "github.com/openshift/hypershift")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(findings).To(Equal([]finding{
				{Package: "github.com/openshift/hypershift/cmd", Function: "Helper", File: "cmd/main.go", Line: 4, Column: 6},
				{Package: "github.com/openshift/hypershift/support", Function: "Alpha", File: "support/a.go", Line: 4, Column: 6},
				{Package: "github.com/openshift/hypershift/support", Function: "Zebra", File: "support/z.go", Line: 4, Column: 6},
			}))
			g.Expect(writeReports(dir, findings, totals, scanMetadata{})).To(Succeed())
			if i == 0 {
				slices.Reverse(packages[0].Funcs)
				slices.Reverse(packages)
			}
		}
		for _, name := range reportNames {
			first, err := os.ReadFile(filepath.Join(artifacts[0], name))
			g.Expect(err).NotTo(HaveOccurred())
			second, err := os.ReadFile(filepath.Join(artifacts[1], name))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(second).To(Equal(first))
		}
	})
}

func testFunction(name, file string, generated bool) analyzerFunction {
	fn := analyzerFunction{Name: name, Generated: &generated, Marker: new(bool)}
	fn.Position.File = file
	fn.Position.Line = 4
	fn.Position.Col = 6
	return fn
}

func writeTestFile(t *testing.T, root, file, contents string) {
	t.Helper()
	g := NewWithT(t)
	path := filepath.Join(root, file)
	g.Expect(os.MkdirAll(filepath.Dir(path), 0755)).To(Succeed())
	g.Expect(os.WriteFile(path, []byte(contents), 0644)).To(Succeed())
}

func TestSourcePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file, want string
		wantError        bool
	}{
		{name: "When a source is relative, it should normalize the path", file: "sub/../source.go", want: "source.go"},
		{name: "When a source is a directory, it should reject the malformed location", file: ".", wantError: true},
		{name: "When a source is missing, it should fail instead of dropping the candidate", file: "missing.go", wantError: true},
		{name: "When a source escapes the repository, it should exclude it", file: "../outside.go"},
		{name: "When a source belongs to another module, it should exclude it", file: "nested/source.go"},
		{name: "When a symlink points outside the repository, it should exclude it", file: "linked.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			root, err := filepath.EvalSymlinks(t.TempDir())
			g.Expect(err).NotTo(HaveOccurred())
			writeTestFile(t, root, "source.go", "package fixture")
			writeTestFile(t, root, "nested/source.go", "package fixture")
			writeTestFile(t, root, "nested/go.mod", "module other")
			outside := filepath.Join(t.TempDir(), "outside.go")
			g.Expect(os.WriteFile(outside, []byte("package fixture"), 0644)).To(Succeed())
			g.Expect(os.Symlink(outside, filepath.Join(root, "linked.go"))).To(Succeed())
			got, err := sourcePath(root, tt.file)
			if tt.wantError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(got).To(Equal(tt.want))
			}
		})
	}
}

func TestWriteReports(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"deadcode.json", "deadcode.txt", "deadcode-summary.txt"} {
		t.Run("When "+name+" cannot be published, it should return an error", func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			artifacts := t.TempDir()
			g.Expect(os.Mkdir(filepath.Join(artifacts, name), 0755)).To(Succeed())
			g.Expect(writeReports(artifacts, []finding{}, scanTotals{}, scanMetadata{})).To(MatchError(ContainSubstring("publishing " + name)))
			_, err := os.ReadFile(filepath.Join(artifacts, "deadcode-summary.txt"))
			g.Expect(err).To(HaveOccurred())
			entries, err := os.ReadDir(artifacts)
			g.Expect(err).NotTo(HaveOccurred())
			for _, entry := range entries {
				g.Expect(entry.Name()).NotTo(HavePrefix(".deadcode-"))
			}
		})
	}
	t.Run("When the artifact directory is missing, it should fail staging the reports", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect(writeReports(filepath.Join(t.TempDir(), "missing"), []finding{}, scanTotals{}, scanMetadata{})).To(MatchError(ContainSubstring("staging")))
	})
}
