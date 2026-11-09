package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

var testChecker string

func TestMain(tests *testing.M) {
	source, err := filepath.Abs("../../..")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testChecker = filepath.Join(source, "hack/tools/bin/crd-schema-check")
	command := exec.CommandContext(context.Background(), "make", "--no-print-directory", testChecker)
	command.Dir = source
	output, err := command.CombinedOutput()
	status := 1
	if err != nil {
		fmt.Fprintf(os.Stderr, "build real schema checker through Make: %v\n%s", err, output)
	} else {
		status = tests.Run()
	}
	os.Exit(status)
}

func makeFixture(t *testing.T) repositoryFixture {
	t.Helper()
	g := NewWithT(t)
	repo := newRepository(t)
	source, err := filepath.Abs("../../..")
	g.Expect(err).NotTo(HaveOccurred())
	writeFile(t, repo.root, "Makefile", "include "+filepath.Join(source, "Makefile")+"\n")
	g.Expect(os.MkdirAll(filepath.Join(repo.root, "hack"), 0755)).To(Succeed())
	g.Expect(os.Symlink(filepath.Join(source, "hack/tools"), filepath.Join(repo.root, "hack/tools"))).To(Succeed())
	writeFile(t, repo.root, "api/go.mod", "module github.com/openshift/hypershift/api\n\ngo 1.26.0\n")
	writeFile(t, repo.root, "stamp", "")
	for _, name := range []string{"lint", "hypershift-plugin", "kube-plugin", "mockgen", "gitlint"} {
		writeFile(t, repo.root, "capture-"+name, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+filepath.Join(repo.root, "arguments")+"'\n")
		g.Expect(os.Chmod(filepath.Join(repo.root, "capture-"+name), 0755)).To(Succeed())
	}
	return repo
}

func makeCommand(t *testing.T, repo repositoryFixture, environment []string, args ...string) (string, error) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "make", append([]string{
		"--no-print-directory",
		"GOLANGCI_LINT=" + filepath.Join(repo.root, "capture-lint"),
		"HYPERSHIFTLINTER_PLUGIN=" + filepath.Join(repo.root, "capture-hypershift-plugin"),
		"KUBEAPILINTER_PLUGIN=" + filepath.Join(repo.root, "capture-kube-plugin"),
		"MOCKGEN=" + filepath.Join(repo.root, "capture-mockgen"),
		"GITLINT=" + filepath.Join(repo.root, "capture-gitlint"),
		"PYTHON_VENV_STAMP=" + filepath.Join(repo.root, "stamp"),
		"CRD_SCHEMA_CHECK=" + testChecker,
	}, args...)...)
	command.Dir = repo.root
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(name, "VERIFICATION_") || strings.HasPrefix(name, "MAKE") || name == "JOB_SPEC" || name == "REPO_OWNER" || name == "REPO_NAME" || strings.HasPrefix(name, "PULL_") {
			continue
		}
		command.Env = append(command.Env, value)
	}
	command.Env = append(command.Env, environment...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestMakeConsumers(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, target string
	}{
		{name: "When API lint runs, it should use the resolved base", target: "api-lint"},
		{name: "When API lint fix runs, it should use the resolved base", target: "api-lint-fix"},
		{name: "When main lint fix runs, it should use the resolved base", target: "main-lint-fix"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			repo := makeFixture(t)
			output, err := makeCommand(t, repo, nil, scenario.target, "PULL_BASE_SHA="+repo.base)
			g.Expect(err).NotTo(HaveOccurred(), "%s", output)
			arguments, err := os.ReadFile(filepath.Join(repo.root, "arguments"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(string(arguments)).To(ContainSubstring("--new-from-rev=" + repo.base))
		})
	}
}

func TestMakeContextPropagation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name      string
		envOrigin bool
		override  bool
	}{
		{name: "When mixed parallel consumers recurse after HEAD moves, it should retain the initial snapshot"},
		{name: "When make -e inherits foreign revisions, it should ignore those environment overrides", envOrigin: true},
		{name: "When command-line revisions recurse through make -e, it should preserve their precedence", envOrigin: true, override: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			repo := makeFixture(t)
			makefile, err := os.ReadFile(filepath.Join(repo.root, "Makefile"))
			g.Expect(err).NotTo(HaveOccurred())
			writeFile(t, repo.root, "Makefile", string(makefile)+"\n.PHONY: advance nested\nadvance:\n\tgit update-ref HEAD "+repo.main+"\nnested: advance\n\t$(MAKE) api-lint run-gitlint\n")
			metadata := jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			environment := []string{"JOB_SPEC=" + metadata, "REPO_OWNER=openshift", "REPO_NAME=release", "PULL_BASE_SHA=foreign-base", "PULL_PULL_SHA=foreign-head"}
			args := []string{"-j2", "api-lint-fix", "nested"}
			if scenario.envOrigin {
				args = append(args, "-e")
			}
			expectedBase, expectedHead := repo.head, repo.head
			if scenario.override {
				args = append(args, "PULL_BASE_SHA="+repo.base, "PULL_PULL_SHA="+repo.head)
				expectedBase = repo.base
			}
			output, err := makeCommand(t, repo, environment, args...)
			g.Expect(err).NotTo(HaveOccurred(), "%s", output)
			arguments, err := os.ReadFile(filepath.Join(repo.root, "arguments"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.Count(string(arguments), "--new-from-rev="+expectedBase)).To(Equal(2))
			if scenario.override {
				g.Expect(string(arguments)).To(ContainSubstring("--commits " + expectedBase + ".." + expectedHead))
			} else {
				g.Expect(string(arguments)).NotTo(ContainSubstring("--commits"))
				g.Expect(output).To(ContainSubstring("empty commit range"))
			}
			g.Expect(fixtureGit(t, repo.root, "rev-parse", "HEAD")).To(Equal(repo.main))
		})
	}
}

func TestMakeSchemaControls(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name     string
		foreign  bool
		breaking bool
	}{
		{name: "When normal CRDs are unchanged, it should pass the real checker"},
		{name: "When normal CRD scope changes, it should reject the scope regression", breaking: true},
		{name: "When rehearsal base equals HEAD and CRDs are unchanged, it should pass the real checker", foreign: true},
		{name: "When rehearsal base equals HEAD and CRD scope changes, it should reject the scope regression", foreign: true, breaking: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			repo := makeFixture(t)
			source, err := filepath.Abs("../../..")
			g.Expect(err).NotTo(HaveOccurred())
			files, err := filepath.Glob(filepath.Join(source, "cmd/install/assets/crds/hypershift-operator/*.yaml"))
			g.Expect(err).NotTo(HaveOccurred())
			var crd []byte
			for _, file := range files {
				crd, err = os.ReadFile(file)
				g.Expect(err).NotTo(HaveOccurred())
				if strings.Contains(string(crd), "scope: Namespaced") {
					break
				}
			}
			g.Expect(string(crd)).To(ContainSubstring("scope: Namespaced"))
			path := "cmd/install/assets/crds/hypershift-operator/fixture.yaml"
			writeFile(t, repo.root, path, string(crd))
			g.Expect(os.MkdirAll(filepath.Join(repo.root, "karpenter-operator/controllers/karpenter/assets/zz_generated.crd-manifests"), 0755)).To(Succeed())
			fixtureGit(t, repo.root, "add", ".")
			fixtureGit(t, repo.root, "commit", "-qm", "ci(verify): add schema fixture")
			base := fixtureGit(t, repo.root, "rev-parse", "HEAD")
			if scenario.breaking {
				writeFile(t, repo.root, path, strings.Replace(string(crd), "scope: Namespaced", "scope: Cluster", 1))
			}
			metadata := jobJSON(t, pullRefs(base, base))
			environment := []string{"JOB_SPEC=" + metadata}
			if scenario.foreign {
				// Synthetic rehearsal: release-repository revisions that do not exist in this checkout.
				foreignBase, foreignHead := strings.Repeat("e", 40), strings.Repeat("d", 40)
				metadata = `{"type":"presubmit","refs":{"org":"openshift","repo":"release","base_ref":"main","base_sha":"` + foreignBase + `","pulls":[{"number":1,"sha":"` + foreignHead + `"}]},"extra_refs":[{"org":"openshift","repo":"hypershift","base_ref":"main","workdir":true}]}`
				environment = []string{"JOB_SPEC=" + metadata, "REPO_OWNER=openshift", "REPO_NAME=release", "PULL_BASE_SHA=" + foreignBase, "PULL_PULL_SHA=" + foreignHead}
			}
			output, err := makeCommand(t, repo, environment, "verify-crd-schema", "run-gitlint")
			g.Expect(output).To(ContainSubstring("--comparison-base=" + base))
			if scenario.breaking {
				g.Expect(err).To(HaveOccurred())
				g.Expect(output).To(ContainSubstring("scope"))
				g.Expect(output).To(ContainSubstring("breaking CRD schema changes detected"))
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "%s", output)
				g.Expect(output).To(ContainSubstring("1 CRDs checked"))
				if scenario.foreign {
					g.Expect(output).To(ContainSubstring("empty commit range"))
				}
			}
		})
	}
}

func TestMakeInvalidContext(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"api-lint", "api-lint-fix", "main-lint-fix", "test-changed", "run-gitlint", "verify-crd-schema"} {
		t.Run("When "+target+" receives an invalid revision, it should fail before invoking a consumer", func(t *testing.T) {
			t.Parallel()
			repo := makeFixture(t)
			output, err := makeCommand(t, repo, nil, target, "PULL_BASE_SHA=missing")
			NewWithT(t).Expect(err).To(HaveOccurred())
			NewWithT(t).Expect(output).To(ContainSubstring("verification context: comparison base"))
			NewWithT(t).Expect(output).NotTo(ContainSubstring("No Go files changed"))
			_, err = os.Stat(filepath.Join(repo.root, "arguments"))
			NewWithT(t).Expect(os.IsNotExist(err)).To(BeTrue())
		})
	}
}

// writeResolver writes a stand-in for $(GO) that records the environment of
// the parse-time resolver invocation and emulates its output and exit status.
func writeResolver(t *testing.T, repo repositoryFixture, output string, status int) string {
	t.Helper()
	path := filepath.Join(repo.root, "capture-go")
	writeFile(t, repo.root, "capture-go", fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$HOME\" >> '%s'\nprintf '%%s' '%s'\nexit %d\n", filepath.Join(repo.root, "resolver-home"), output, status))
	NewWithT(t).Expect(os.Chmod(path, 0755)).To(Succeed())
	return path
}

// makeSupportsShellStatus reports whether the GNU Make in use sets
// .SHELLSTATUS, which was added in GNU Make 4.2.
func makeSupportsShellStatus(t *testing.T) bool {
	t.Helper()
	command := exec.CommandContext(t.Context(), "make", "--no-print-directory", "-f", "-")
	command.Stdin = strings.NewReader("status := $(shell exit 3)\n$(info status=$(.SHELLSTATUS))\nall: ;@:\n")
	output, err := command.CombinedOutput()
	NewWithT(t).Expect(err).NotTo(HaveOccurred(), "%s", output)
	return strings.Contains(string(output), "status=3")
}

func TestMakeResolverInvocation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, diagnostic, absent string
		emptyOutput              bool
		status                   int
		requiresShellStatus      bool
	}{
		{name: "When HOME is the unwritable root, it should resolve with the Makefile's writable HOME"},
		{name: "When the resolver exits successfully without context, it should fail closed on its output", emptyOutput: true, diagnostic: "verification context resolution failed or produced invalid output"},
		{name: "When the resolver fails without output, it should fail before invoking consumers", emptyOutput: true, status: 1, diagnostic: "verification context resolution failed"},
		{name: "When the resolver fails after printing a context, it should stop on its exit status", status: 1, diagnostic: "verification context resolution failed", absent: "or produced invalid output", requiresShellStatus: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			if scenario.requiresShellStatus && !makeSupportsShellStatus(t) {
				t.Skip("GNU Make before 4.2 has no .SHELLSTATUS; the output check covers it")
			}
			g := NewWithT(t)
			repo := makeFixture(t)
			resolved := ""
			if !scenario.emptyOutput {
				resolved = strings.Join([]string{repo.base, repo.head, "local", repo.head, repo.base}, " ") + "\n"
			}
			resolver := writeResolver(t, repo, resolved, scenario.status)
			// HOME=/ matches CI runners; GNU Make before 4.4 does not pass the
			// Makefile's rewritten HOME to $(shell) unless it is set explicitly.
			output, err := makeCommand(t, repo, []string{"HOME=/"}, "api-lint", "GO="+resolver)
			home, readErr := os.ReadFile(filepath.Join(repo.root, "resolver-home"))
			g.Expect(readErr).NotTo(HaveOccurred())
			g.Expect(string(home)).To(Equal("/tmp\n"))
			arguments, argumentsErr := os.ReadFile(filepath.Join(repo.root, "arguments"))
			if scenario.diagnostic != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(output).To(ContainSubstring(scenario.diagnostic))
				if scenario.absent != "" {
					g.Expect(output).NotTo(ContainSubstring(scenario.absent))
				}
				g.Expect(os.IsNotExist(argumentsErr)).To(BeTrue())
				return
			}
			g.Expect(err).NotTo(HaveOccurred(), "%s", output)
			g.Expect(argumentsErr).NotTo(HaveOccurred())
			g.Expect(string(arguments)).To(ContainSubstring("--new-from-rev=" + repo.base))
		})
	}
}

func TestMakeUnlistedConsumer(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	repo := makeFixture(t)
	makefile, err := os.ReadFile(filepath.Join(repo.root, "Makefile"))
	g.Expect(err).NotTo(HaveOccurred())
	// A goal missing from VERIFICATION_GOALS does not resolve a context, so a
	// consumer it reaches must fail instead of skipping with an empty base.
	writeFile(t, repo.root, "Makefile", string(makefile)+"\n.PHONY: unlisted-tests\nunlisted-tests: test-changed\n")
	output, err := makeCommand(t, repo, nil, "unlisted-tests")
	g.Expect(err).To(HaveOccurred())
	g.Expect(output).To(ContainSubstring("verification context: no comparison base for test-changed"))
	g.Expect(output).NotTo(ContainSubstring("No Go files changed"))
}

func TestMakeDeclaredContexts(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name         string
		batch        bool
		environment  func(*testing.T, repositoryFixture) []string
		base, commit func(repo repositoryFixture, checkout string) string
	}{
		{
			name:  "When a Tide batch declares multiple pulls, it should lint against the exact base and merged checkout",
			batch: true,
			environment: func(t *testing.T, repo repositoryFixture) []string {
				return []string{"JOB_SPEC=" + jobJSON(t, batchRefs(repo.base, repo.head, repo.main)), "REPO_OWNER=openshift", "REPO_NAME=hypershift", "PULL_BASE_SHA=" + repo.base}
			},
			base:   func(repo repositoryFixture, _ string) string { return repo.base },
			commit: func(repo repositoryFixture, checkout string) string { return repo.base + ".." + checkout },
		},
		{
			name: "When a private mirror hosts the PR, it should lint against its declared revisions",
			environment: func(t *testing.T, repo repositoryFixture) []string {
				primary := pullRefs(repo.base, repo.head)
				primary.Org = mirrorOrg
				return []string{"JOB_SPEC=" + jobJSON(t, primary), "REPO_OWNER=" + mirrorOrg, "REPO_NAME=hypershift", "PULL_BASE_SHA=" + repo.base, "PULL_PULL_SHA=" + repo.head}
			},
			base:   func(repo repositoryFixture, _ string) string { return repo.base },
			commit: func(repo repositoryFixture, _ string) string { return repo.base + ".." + repo.head },
		},
		{
			name:        "When a periodic job declares no primary refs, it should lint against upstream main",
			environment: func(*testing.T, repositoryFixture) []string { return []string{"JOB_SPEC=" + periodicJobSpec} },
			base:        func(repo repositoryFixture, _ string) string { return repo.main },
			commit:      func(repo repositoryFixture, checkout string) string { return repo.main + ".." + checkout },
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			repo := makeFixture(t)
			checkout := repo.head
			if scenario.batch {
				checkout = mergeBatch(t, repo)
			}
			output, err := makeCommand(t, repo, scenario.environment(t, repo), "api-lint", "run-gitlint")
			g.Expect(err).NotTo(HaveOccurred(), "%s", output)
			arguments, err := os.ReadFile(filepath.Join(repo.root, "arguments"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(string(arguments)).To(ContainSubstring("--new-from-rev=" + scenario.base(repo, checkout)))
			g.Expect(string(arguments)).To(ContainSubstring("--commits " + scenario.commit(repo, checkout)))
		})
	}
}

func TestMakeCommitRange(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, message string
		foreign       bool
		fail          bool
	}{
		{name: "When a real normal PR commit is valid, it should lint that exact range", message: "ci(verify): change fixture\n\nExercise real commit lint validation.\n"},
		{name: "When a real normal PR commit is invalid, it should reject it", message: "invalid commit", fail: true},
		{name: "When a no-pulls extra-ref has a nonempty supplied-base range, it should still lint commits", message: "invalid commit", foreign: true, fail: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			repo := makeFixture(t)
			base := repo.head
			writeFile(t, repo.root, "value.txt", "new commit\n")
			fixtureGit(t, repo.root, "commit", "-qam", scenario.message)
			head := fixtureGit(t, repo.root, "rev-parse", "HEAD")
			primary := pullRefs(base, head)
			metadata := jobJSON(t, primary)
			if scenario.foreign {
				extra := pullRefs(base, "")
				extra.Workdir = true
				metadata = jobJSON(t, refs{Org: "openshift", Repo: "release"}, extra)
			}
			config, err := os.ReadFile("../../../.gitlint")
			g.Expect(err).NotTo(HaveOccurred())
			writeFile(t, repo.root, ".gitlint", string(config))
			gitlint, err := filepath.Abs("../bin/python-venv/bin/gitlint")
			g.Expect(err).NotTo(HaveOccurred())
			output, err := makeCommand(t, repo, []string{"JOB_SPEC=" + metadata}, "run-gitlint", "GITLINT="+gitlint)
			g.Expect(output).To(ContainSubstring("Linting commits from " + base + " to " + head))
			g.Expect(output).NotTo(ContainSubstring("empty commit range"))
			if scenario.fail {
				g.Expect(err).To(HaveOccurred())
				g.Expect(output).To(ContainSubstring("CT1 Title does not follow ConventionalCommits.org"))
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "%s", output)
			}
		})
	}
}

func TestMakeChangedTests(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name string
		fail bool
	}{
		{name: "When a changed Go package passes, it should run its tests"},
		{name: "When a changed Go package fails, it should propagate the failure", fail: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			repo := makeFixture(t)
			writeFile(t, repo.root, "support/fixture/value.go", "package fixture\nfunc Value() int { return 1 }\n")
			body := ""
			if scenario.fail {
				body = `t.Fatal("changed package test failed")`
			}
			writeFile(t, repo.root, "support/fixture/value_test.go", "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { "+body+" }\n")
			fixtureGit(t, repo.root, "add", ".")
			fixtureGit(t, repo.root, "commit", "-qm", "ci(verify): add changed Go fixture")
			output, err := makeCommand(t, repo, nil, "test-changed", "PULL_BASE_SHA="+repo.head)
			g.Expect(output).To(ContainSubstring("Running tests for changed packages:"))
			g.Expect(output).To(ContainSubstring("./support/fixture"))
			if scenario.fail {
				g.Expect(err).To(HaveOccurred())
				g.Expect(output).To(ContainSubstring("changed package test failed"))
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "%s", output)
			}
		})
	}
}

func TestMakeFrozenChangedSelection(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)
	repo := makeFixture(t)
	writeFile(t, repo.root, "support/selected/value.go", "package selected\nfunc Value() int { return 1 }\n")
	writeFile(t, repo.root, "support/selected/value_test.go", "package selected\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value()!=1 { t.Fatal(\"selected failed\") } }\n")
	fixtureGit(t, repo.root, "add", "support/selected")
	fixtureGit(t, repo.root, "commit", "-qm", "ci(verify): add selected package")
	selected := fixtureGit(t, repo.root, "rev-parse", "HEAD")
	writeFile(t, repo.root, "support/unselected/value.go", "package unselected\n")
	writeFile(t, repo.root, "support/unselected/value_test.go", "package unselected\nimport \"testing\"\nfunc TestValue(t *testing.T) { t.Fatal(\"must not select checkout-only package\") }\n")
	fixtureGit(t, repo.root, "add", "support/unselected")
	fixtureGit(t, repo.root, "commit", "-qm", "ci(verify): add checkout-only package")
	makefile, err := os.ReadFile(filepath.Join(repo.root, "Makefile"))
	g.Expect(err).NotTo(HaveOccurred())
	writeFile(t, repo.root, "Makefile", string(makefile)+"\n.PHONY: nested-tests\nnested-tests:\n\tgit update-ref HEAD "+repo.main+"\n\t$(MAKE) test-changed\n")
	metadata := jobJSON(t, pullRefs(repo.head, selected))
	output, err := makeCommand(t, repo, []string{"JOB_SPEC=" + metadata}, "-j2", "test-changed", "nested-tests")
	g.Expect(err).NotTo(HaveOccurred(), "%s", output)
	normalized := strings.Join(strings.Fields(output), " ")
	g.Expect(strings.Count(normalized, "Running tests for changed packages: ./support/selected")).To(Equal(2), "%s", output)
	g.Expect(output).NotTo(ContainSubstring("./support/unselected"))
	g.Expect(fixtureGit(t, repo.root, "rev-parse", "HEAD")).To(Equal(repo.main))
}

func TestMakeUnrelatedDiff(t *testing.T) {
	t.Parallel()
	repo := makeFixture(t)
	fixtureGit(t, repo.root, "switch", "--orphan", "isolated")
	writeFile(t, repo.root, "isolated.txt", "unrelated history\n")
	fixtureGit(t, repo.root, "add", "isolated.txt")
	fixtureGit(t, repo.root, "commit", "-qm", "ci(verify): create unrelated history")
	output, err := makeCommand(t, repo, nil, "test-changed", "PULL_BASE_SHA="+repo.head)
	NewWithT(t).Expect(err).To(HaveOccurred())
	NewWithT(t).Expect(output).To(ContainSubstring("no merge base"))
	NewWithT(t).Expect(output).NotTo(ContainSubstring("No Go files changed"))
}

func TestMakeEmptyLocalBase(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, target, diagnostic string
		fail                     bool
	}{
		{name: "When local lint base is intentionally empty, it should run unfiltered", target: "main-lint-fix"},
		{name: "When schema base is empty, it should fail rather than skip comparison", target: "verify-crd-schema", fail: true, diagnostic: "requires a comparison base"},
		{name: "When test selection base is empty, it should fail rather than skip tests", target: "test-changed", fail: true, diagnostic: "requires a comparison base"},
		{name: "When no gitlint merge base exists, it should fail rather than invent HEAD parent", target: "run-gitlint", fail: true, diagnostic: "no local commit comparison base"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			repo := makeFixture(t)
			fixtureGit(t, repo.root, "update-ref", "-d", "refs/remotes/origin/main")
			output, err := makeCommand(t, repo, nil, scenario.target, "PULL_BASE_SHA=")
			if scenario.fail {
				g.Expect(err).To(HaveOccurred())
				g.Expect(output).To(ContainSubstring(scenario.diagnostic))
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "%s", output)
				arguments, err := os.ReadFile(filepath.Join(repo.root, "arguments"))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(arguments)).NotTo(ContainSubstring("--new-from-rev"))
			}
		})
	}
}

func TestMakeMalformedContext(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, metadata, diagnostic string
	}{
		{name: "When primary pulls are missing, it should fail before consumers", metadata: `{"refs":{"org":"openshift","repo":"hypershift","base_sha":"@BASE@"}}`, diagnostic: "requires a declared pull head"},
		{name: "When refs has a case alias, it should fail before selecting primary context", metadata: `{"refs":{"org":"openshift","repo":"release"},"Refs":{"org":"openshift","repo":"hypershift","base_sha":"@BASE@","pulls":[{"sha":"@HEAD@"}]}}`, diagnostic: "noncanonical context field"},
		{name: "When repository identity has a case alias, it should reject the ambiguity", metadata: `{"refs":{"org":"openshift","repo":"release","Repo":"hypershift","base_sha":"@BASE@","pulls":[{"sha":"@HEAD@"}]}}`, diagnostic: "noncanonical context field"},
		{name: "When primary and extra-ref workdirs conflict, it should reject the contradiction", metadata: `{"refs":{"org":"openshift","repo":"release","workdir":true},"extra_refs":[{"org":"openshift","repo":"hypershift","workdir":true}]}`, diagnostic: "conflicting primary workdir"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			repo := makeFixture(t)
			metadata := strings.NewReplacer("@BASE@", repo.base, "@HEAD@", repo.head).Replace(scenario.metadata)
			output, err := makeCommand(t, repo, []string{"JOB_SPEC=" + metadata}, "api-lint", "verify-crd-schema", "run-gitlint")
			NewWithT(t).Expect(err).To(HaveOccurred())
			NewWithT(t).Expect(output).To(ContainSubstring(scenario.diagnostic))
			_, err = os.Stat(filepath.Join(repo.root, "arguments"))
			NewWithT(t).Expect(os.IsNotExist(err)).To(BeTrue())
		})
	}
}
