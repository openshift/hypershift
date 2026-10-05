package main

import (
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestRun(t *testing.T) {
	t.Parallel()
	t.Run("When the pinned analyzer scans a fixture, it should publish only unreachable first-party functions", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		root := t.TempDir()
		g.Expect(os.CopyFS(root, os.DirFS("testdata/reachability"))).To(Succeed())
		g.Expect(exec.CommandContext(t.Context(), "git", "init", "-q", root).Run()).To(Succeed())
		g.Expect(exec.CommandContext(t.Context(), "git", "-C", root, "add", ".").Run()).To(Succeed())
		g.Expect(exec.CommandContext(t.Context(), "git", "-C", root, "-c", "commit.gpgsign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@redhat.com", "commit", "-qm", "fixture").Run()).To(Succeed())
		artifacts := filepath.Join(t.TempDir(), "reports with spaces")
		// A selected executable path is a literal filename, not shell syntax.
		tool := filepath.Join(t.TempDir(), "deadcode $(touch unexpected-command)")
		binary, err := os.ReadFile(buildTestTool(t, "golang.org/x/tools/cmd/deadcode"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(os.WriteFile(tool, binary, 0755)).To(Succeed())
		g.Expect(run(t.Context(), root, tool, artifacts)).To(Succeed())
		_, err = os.Stat(filepath.Join(root, "unexpected-command"))
		g.Expect(os.IsNotExist(err)).To(BeTrue())
		data, err := os.ReadFile(filepath.Join(artifacts, "deadcode.json"))
		g.Expect(err).NotTo(HaveOccurred())
		var findings []finding
		g.Expect(json.Unmarshal(data, &findings)).To(Succeed())
		g.Expect(findings).To(Equal([]finding{{Package: "github.com/openshift/hypershift", Function: "DeadExported", File: "main.go", Line: 19, Column: 6}}))
		text, err := os.ReadFile(filepath.Join(artifacts, "deadcode.txt"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(text)).To(ContainSubstring("main.go:19:6: github.com/openshift/hypershift.DeadExported"))
		summary, err := os.ReadFile(filepath.Join(artifacts, "deadcode-summary.txt"))
		g.Expect(err).NotTo(HaveOccurred())
		for _, expected := range []string{"v0.44.0", "GOOS=linux GOARCH=arm64 CGO_ENABLED=0", "envtest", "Reported candidates: 1", "Generated exclusions: 1", "Vendor exclusions: 1", "Copied-code exclusions: 1", "integration,e2e,reqserving,e2ev2,backuprestore"} {
			g.Expect(string(summary)).To(ContainSubstring(expected))
		}
		g.Expect(string(summary)).To(ContainSubstring("Tracked source dirty: false"))
		writeTestFile(t, root, "main_test.go", "package main\n")
		g.Expect(os.Remove(filepath.Join(root, "generated.go"))).To(Succeed())
		g.Expect(os.Remove(filepath.Join(root, "tagged_test.go"))).To(Succeed())
		// Keep dependency and secondary executable declarations live in the empty scan.
		writeTestFile(t, root, "main.go", "package main\nimport (\"github.com/openshift/hypershift/api\"; copied \"github.com/openshift/hypershift/support/thirdparty\")\nfunc main() { api.PublicDead(); copied.CopiedDead() }\n")
		g.Expect(run(t.Context(), root, tool, artifacts)).To(Succeed())
		data, err = os.ReadFile(filepath.Join(artifacts, "deadcode.json"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(data)).To(Equal("[]\n"))
		summary, err = os.ReadFile(filepath.Join(artifacts, "deadcode-summary.txt"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(summary)).To(ContainSubstring("Reported candidates: 0"))
		g.Expect(string(summary)).To(ContainSubstring("Tracked source dirty: true"))
		reporter := buildTestTool(t, "./deadcode-report")
		cmd := exec.CommandContext(t.Context(), reporter, "-deadcode="+tool, "-artifact-dir="+artifacts)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOMEMLIMIT=4GiB", "GOGC=70", "GOPROXY=off", "GOSUMDB=off", "GOPACKAGESDRIVER=/nonexistent-driver")
		out, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred(), "%s", out)
		summary, err = os.ReadFile(filepath.Join(artifacts, "deadcode-summary.txt"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(summary)).To(ContainSubstring("GOMEMLIMIT=4GiB GOGC=70"))
		g.Expect(string(summary)).To(ContainSubstring("GOPACKAGESDRIVER=off"))
		writeTestFile(t, root, "main.go", "package main\nfunc main() { undefined() }\n")
		g.Expect(run(t.Context(), root, tool, artifacts)).To(MatchError(ContainSubstring("analysis failed")))
		_, err = os.Stat(filepath.Join(artifacts, "deadcode-summary.txt"))
		g.Expect(os.IsNotExist(err)).To(BeTrue())
		cmd = exec.CommandContext(t.Context(), reporter, "-deadcode="+tool, "-artifact-dir="+artifacts)
		cmd.Dir = root
		out, err = cmd.CombinedOutput()
		var exitError *exec.ExitError
		g.Expect(errors.As(err, &exitError)).To(BeTrue(), "%s", out)
		g.Expect(exitError.ExitCode()).To(Equal(1))
		g.Expect(string(out)).To(ContainSubstring("packages contain errors"))
	})
	for _, scenario := range []string{"missing analyzer", "invalid module", "unwritable artifact path"} {
		t.Run("When there is a "+scenario+", it should fail without a completion report", func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			root := t.TempDir()
			writeTestFile(t, root, "go.mod", "module github.com/openshift/hypershift\n\ngo 1.26.0\n")
			artifacts := filepath.Join(t.TempDir(), "artifacts")
			switch scenario {
			case "invalid module":
				writeTestFile(t, root, "go.mod", "invalid go.mod")
			case "unwritable artifact path":
				g.Expect(os.WriteFile(artifacts, nil, 0644)).To(Succeed())
			}
			g.Expect(run(t.Context(), root, filepath.Join(root, "missing"), artifacts)).To(HaveOccurred())
			_, err := os.Stat(filepath.Join(artifacts, "deadcode-summary.txt"))
			g.Expect(err).To(HaveOccurred())
		})
	}
	t.Run("When stale report cleanup fails, it should invalidate the old success summary first", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		root, artifacts := t.TempDir(), t.TempDir()
		writeTestFile(t, root, "go.mod", "module github.com/openshift/hypershift\n")
		writeTestFile(t, artifacts, "deadcode-summary.txt", "previous success")
		g.Expect(os.Mkdir(filepath.Join(artifacts, "deadcode.txt"), 0755)).To(Succeed())
		g.Expect(run(t.Context(), root, "missing", artifacts)).To(MatchError(ContainSubstring("is a directory")))
		_, err := os.Stat(filepath.Join(artifacts, "deadcode-summary.txt"))
		g.Expect(os.IsNotExist(err)).To(BeTrue())
	})
}

func TestAnalyzerVersion(t *testing.T) {
	t.Parallel()
	t.Run("When the executable is the pinned analyzer, it should identify its version", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		version, err := analyzerVersion(buildTestTool(t, "golang.org/x/tools/cmd/deadcode"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(version).To(Equal("v0.44.0"))
	})
	t.Run("When the executable is not a Go analyzer, it should reject it", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		file := filepath.Join(t.TempDir(), "not-deadcode")
		g.Expect(os.WriteFile(file, []byte("not an executable"), 0644)).To(Succeed())
		_, err := analyzerVersion(file)
		g.Expect(err).To(MatchError(ContainSubstring("build information")))
	})
	t.Run("When the executable is a different Go command, it should reject its identity", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		_, err := analyzerVersion(buildTestTool(t, "./deadcode-report"))
		g.Expect(err).To(MatchError(ContainSubstring("analyzer must be")))
	})
}

func TestDeadcode(t *testing.T) {
	t.Parallel()
	for _, prerequisite := range []struct{ name, target string }{{"mock generation", "generate"}, {"analyzer build", "$(DEADCODE)"}} {
		t.Run("When the "+prerequisite.name+" prerequisite fails, it should make the command fail", func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			root, err := filepath.Abs("../../..")
			g.Expect(err).NotTo(HaveOccurred())
			fixture := t.TempDir()
			makefile := "include " + filepath.Join(root, "Makefile") + "\n" + prerequisite.target + ":\n\t@echo fixture prerequisite failure >&2\n\t@exit 9\n"
			writeTestFile(t, fixture, "Makefile", makefile)
			cmd := exec.CommandContext(t.Context(), "make", "-f", filepath.Join(fixture, "Makefile"), "deadcode", "TOOLS_DIR="+filepath.Join(root, "hack/tools"), "DEADCODE="+filepath.Join(fixture, "deadcode"), "DEADCODE_REPORT="+filepath.Join(fixture, "deadcode-report"), "MOCKGEN="+filepath.Join(fixture, "mockgen"), "ARTIFACT_DIR="+filepath.Join(fixture, "artifacts"))
			// Prevent other recipes from running: this fixture isolates prerequisite propagation.
			writeTestFile(t, fixture, "deadcode-report", "")
			writeTestFile(t, fixture, "mockgen", "")
			if prerequisite.target == "generate" {
				writeTestFile(t, fixture, "deadcode", "")
			}
			cmd.Dir = fixture
			out, err := cmd.CombinedOutput()
			g.Expect(err).To(HaveOccurred())
			g.Expect(string(out)).To(ContainSubstring("fixture prerequisite failure"))
			_, err = os.Stat(filepath.Join(fixture, "artifacts/deadcode-summary.txt"))
			g.Expect(os.IsNotExist(err)).To(BeTrue())
		})
	}
	foreignOS, foreignArch := "linux", "amd64"
	if runtime.GOOS == foreignOS && runtime.GOARCH == foreignArch {
		foreignArch = "arm64"
	}
	for _, scenario := range []struct {
		name      string
		inherited bool
		cached    bool
	}{
		{name: "cross-compilation is inherited", inherited: true},
		{name: "cross-compilation is persisted only in GOENV"},
		{name: "an up-to-date mockgen is cross-compiled", cached: true},
	} {
		t.Run("When "+scenario.name+", it should build and generate with native tools", func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			root, err := filepath.Abs("../../..")
			g.Expect(err).NotTo(HaveOccurred())
			fixture := t.TempDir()
			g.Expect(os.CopyFS(fixture, os.DirFS("testdata/reachability"))).To(Succeed())
			g.Expect(exec.CommandContext(t.Context(), "git", "init", "-q", fixture).Run()).To(Succeed())
			g.Expect(exec.CommandContext(t.Context(), "git", "-C", fixture, "-c", "commit.gpgsign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@redhat.com", "commit", "--allow-empty", "-qm", "fixture").Run()).To(Succeed())
			goenv := filepath.Join(fixture, "goenv")
			writeTestFile(t, fixture, "goenv", "")
			// Remove inherited targets so the persisted-only case really uses GOENV.
			var env []string
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "GOOS=") && !strings.HasPrefix(entry, "GOARCH=") && !strings.HasPrefix(entry, "GOENV=") {
					env = append(env, entry)
				}
			}
			env = append(env, "GOENV="+goenv, "GOPROXY=off", "GOSUMDB=off")
			if scenario.inherited {
				env = append(env, "GOOS="+foreignOS, "GOARCH="+foreignArch)
			} else if !scenario.cached {
				writeTestFile(t, fixture, "goenv", "GOOS="+foreignOS+"\nGOARCH="+foreignArch+"\n")
			}
			tools := filepath.Join(fixture, "tools")
			g.Expect(os.Mkdir(tools, 0755)).To(Succeed())
			for _, name := range []string{"go.mod", "go.sum", "tools.go", "vendor", "deadcode-report"} {
				g.Expect(os.Symlink(filepath.Join(root, "hack/tools", name), filepath.Join(tools, name))).To(Succeed())
			}
			if scenario.cached {
				mockgen := filepath.Join(tools, "bin/mockgen")
				cmd := exec.CommandContext(t.Context(), "go", "build", "-o", mockgen, "go.uber.org/mock/mockgen")
				cmd.Dir = tools
				cmd.Env = append(append([]string{}, env...), "GO111MODULE=on", "GOWORK=off", "GOFLAGS=-mod=vendor", "CGO_ENABLED=0", "GOOS="+foreignOS, "GOARCH="+foreignArch)
				out, err := cmd.CombinedOutput()
				g.Expect(err).NotTo(HaveOccurred(), "%s", out)
				info, err := buildinfo.ReadFile(mockgen)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(info.Settings).To(ContainElement(And(HaveField("Key", "GOOS"), HaveField("Value", foreignOS))))
				g.Expect(info.Settings).To(ContainElement(And(HaveField("Key", "GOARCH"), HaveField("Value", foreignArch))))
				moduleInfo, err := os.Stat(filepath.Join(tools, "go.mod"))
				g.Expect(err).NotTo(HaveOccurred())
				newer := moduleInfo.ModTime().Add(time.Hour)
				g.Expect(os.Chtimes(mockgen, newer, newer)).To(Succeed())
			}
			makefile := "include " + filepath.Join(root, "Makefile") + "\ngenerate:\n\t@test \"$$GOOS/$$GOARCH\" = \"" + runtime.GOOS + "/" + runtime.GOARCH + "\"\n\t@$(MOCKGEN) -source=main.go -destination=$(TOOLS_DIR)/fixture_mock.go -package=main\n"
			writeTestFile(t, fixture, "Makefile", makefile)
			cmd := exec.CommandContext(t.Context(), "make", "deadcode", "TOOLS_DIR="+tools, "PULL_BASE_SHA=HEAD", "ARTIFACT_DIR="+filepath.Join(fixture, "artifacts"))
			cmd.Dir = fixture
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			g.Expect(err).NotTo(HaveOccurred(), "%s", out)
			mock, err := os.ReadFile(filepath.Join(tools, "fixture_mock.go"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(string(mock)).To(ContainSubstring("Code generated by MockGen. DO NOT EDIT."))
			for _, name := range []string{"deadcode", "deadcode-report", "mockgen"} {
				info, err := buildinfo.ReadFile(filepath.Join(tools, "bin", name))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(info.Settings).To(ContainElement(And(HaveField("Key", "GOOS"), HaveField("Value", runtime.GOOS))))
				g.Expect(info.Settings).To(ContainElement(And(HaveField("Key", "GOARCH"), HaveField("Value", runtime.GOARCH))))
			}
		})
	}
}

func buildTestTool(t *testing.T, pkg string) string {
	t.Helper()
	// Build from the checked-in tools vendor tree; never download tools in tests.
	g := NewWithT(t)
	tool := filepath.Join(t.TempDir(), "tool")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", tool, pkg)
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "GO111MODULE=on", "GOWORK=off", "GOFLAGS=-mod=vendor", "GOPROXY=off", "GOSUMDB=off", "GOOS=", "GOARCH=")
	out, err := cmd.CombinedOutput()
	g.Expect(err).NotTo(HaveOccurred(), "%s", out)
	return tool
}
