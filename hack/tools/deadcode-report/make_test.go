package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

func TestTestChanged(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name         string
		changeRoot   bool
		failRootTest bool
	}{
		{name: "When only the separate tools module changes, it should not load it from the root module"},
		{name: "When a root package and the tools module change, it should still test the root package", changeRoot: true},
		{name: "When a changed root package has a failing test, it should fail the command", changeRoot: true, failRootTest: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			repo, err := filepath.Abs("../../..")
			g.Expect(err).NotTo(HaveOccurred())
			fixture := t.TempDir()
			writeTestFile(t, fixture, "go.mod", "module github.com/openshift/hypershift\n\ngo 1.26.0\n")
			writeTestFile(t, fixture, "support/fixture/value.go", "package fixture\nfunc Value() int { return 1 }\n")
			writeTestFile(t, fixture, "support/fixture/value_test.go", "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(\"root test failure must propagate\") } }\n")
			writeTestFile(t, fixture, "hack/tools/go.mod", "module github.com/openshift/hypershift/hack/tools\n\ngo 1.26.0\n")
			writeTestFile(t, fixture, "hack/tools/tools.go", "package tools\n")
			writeTestFile(t, fixture, "hack/tools/reporter/main.go", "package main\nfunc main() {}\n")
			writeTestFile(t, fixture, "Makefile", "include "+filepath.Join(repo, "Makefile")+"\n")
			git := func(args ...string) string {
				cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", fixture, "-c", "commit.gpgsign=false"}, args...)...)
				out, err := cmd.CombinedOutput()
				g.Expect(err).NotTo(HaveOccurred(), "%s", out)
				return strings.TrimSpace(string(out))
			}
			git("init", "-q")
			git("add", ".")
			git("-c", "user.name=Fixture", "-c", "user.email=fixture@redhat.com", "commit", "-qm", "fixture base")
			base := git("rev-parse", "HEAD")
			writeTestFile(t, fixture, "hack/tools/tools.go", "package tools\n// Changed tool registration.\n")
			writeTestFile(t, fixture, "hack/tools/reporter/main.go", "package main\nfunc main() {}\n// Changed reporting tool.\n")
			if scenario.changeRoot {
				value := "1"
				if scenario.failRootTest {
					value = "2"
				}
				writeTestFile(t, fixture, "support/fixture/value.go", "package fixture\nfunc Value() int { return "+value+" }\n// Changed root package.\n")
			}
			git("add", ".")
			git("-c", "user.name=Fixture", "-c", "user.email=fixture@redhat.com", "commit", "-qm", "fixture change")
			cmd := exec.CommandContext(t.Context(), "make", "test-changed", "PULL_BASE_SHA="+base)
			cmd.Dir = fixture
			cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOOS=", "GOARCH=")
			out, err := cmd.CombinedOutput()
			if scenario.failRootTest {
				g.Expect(err).To(HaveOccurred())
				g.Expect(string(out)).To(ContainSubstring("root test failure must propagate"))
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "%s", out)
			}
			if scenario.changeRoot {
				g.Expect(strings.Join(strings.Fields(string(out)), " ")).To(ContainSubstring("Running tests for changed packages: ./support/fixture"))
				if !scenario.failRootTest {
					g.Expect(strings.Join(strings.Fields(string(out)), " ")).To(ContainSubstring("ok github.com/openshift/hypershift/support/fixture"))
				}
			} else {
				g.Expect(string(out)).To(ContainSubstring("No Go files changed"))
			}
		})
	}
}
