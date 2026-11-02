package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

type repositoryFixture struct {
	root, base, head, main string
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	NewWithT(t).Expect(os.MkdirAll(filepath.Dir(path), 0755)).To(Succeed())
	NewWithT(t).Expect(os.WriteFile(path, []byte(content), 0644)).To(Succeed())
}

func fixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", append([]string{"-C", root, "-c", "commit.gpgsign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@redhat.com"}, args...)...)
	output, err := command.CombinedOutput()
	NewWithT(t).Expect(err).NotTo(HaveOccurred(), "%s", output)
	return strings.TrimSpace(string(output))
}

func newRepository(t *testing.T) repositoryFixture {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module github.com/openshift/hypershift\n\ngo 1.26.0\n")
	writeFile(t, root, "value.txt", "base\n")
	writeFile(t, root, "identity.txt", root+"\n")
	fixtureGit(t, root, "init", "-qb", "main")
	fixtureGit(t, root, "add", ".")
	fixtureGit(t, root, "commit", "-qm", "ci(verify): add fixture base")
	base := fixtureGit(t, root, "rev-parse", "HEAD")
	fixtureGit(t, root, "switch", "-qc", "topic")
	writeFile(t, root, "value.txt", "head\n")
	fixtureGit(t, root, "commit", "-qam", "ci(verify): change fixture")
	head := fixtureGit(t, root, "rev-parse", "HEAD")
	fixtureGit(t, root, "switch", "-q", "main")
	writeFile(t, root, "main.txt", "newer main\n")
	fixtureGit(t, root, "add", ".")
	fixtureGit(t, root, "commit", "-qm", "ci(verify): advance main")
	main := fixtureGit(t, root, "rev-parse", "HEAD")
	fixtureGit(t, root, "switch", "-q", "topic")
	fixtureGit(t, root, "remote", "add", "upstream", "https://github.com/openshift/hypershift.git")
	fixtureGit(t, root, "update-ref", "refs/remotes/upstream/main", main)
	fixtureGit(t, root, "update-ref", "refs/remotes/origin/main", base)
	return repositoryFixture{root: root, base: base, head: head, main: main}
}

func jobJSON(t *testing.T, primary refs, extra ...refs) string {
	t.Helper()
	encoded, err := json.Marshal(struct {
		Refs      refs   `json:"refs"`
		ExtraRefs []refs `json:"extra_refs,omitempty"`
	}{primary, extra})
	NewWithT(t).Expect(err).NotTo(HaveOccurred())
	return string(encoded)
}

func pullRefs(base, head string) refs {
	result := refs{Org: "openshift", Repo: "hypershift", BaseSHA: base}
	if head != "" {
		result.Pulls = append(result.Pulls, struct {
			SHA string `json:"sha"`
		}{head})
	}
	return result
}

func TestResolveContext(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name         string
		modify       func(*testing.T, repositoryFixture, *inputs)
		base         string
		head         string
		mode         string
		err          string
		commitBase   string
		noCommitBase bool
	}{
		{name: "When local inputs are absent, it should resolve the configured upstream main", base: "main", head: "head", mode: "local"},
		{name: "When there is no upstream, it should use local main", modify: func(_ *testing.T, _ repositoryFixture, input *inputs) { input.upstream = "" }, base: "main", head: "head", mode: "local"},
		{name: "When a normal PR targets an older non-main base, it should retain exact base and pull head", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, pullRefs(repo.base, repo.head))
			input.checkout = repo.main
		}, base: "base", head: "head", mode: "primary"},
		{name: "When a recorded foreign extra-ref has no pulls or base, it should use the checkout snapshot", modify: func(t *testing.T, _ repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			input.base, input.head = strings.Repeat("a", 40), strings.Repeat("b", 40)
			input.baseOrigin, input.headOrigin = "environment", "environment override"
		}, base: "head", head: "head", mode: "extra-ref-no-pulls"},
		{name: "When foreign objects exist locally, it should still use the repository-authorized snapshot", modify: func(t *testing.T, _ repositoryFixture, input *inputs) {
			foreign := newRepository(t)
			fixtureGit(t, input.root, "fetch", "-q", foreign.root, "topic")
			input.jobSpec = jobJSON(t, pullRefs(foreign.base, foreign.head), refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			input.jobSpec = strings.Replace(input.jobSpec, `"repo":"hypershift"`, `"repo":"release"`, 1)
			input.base, input.head = foreign.base, foreign.head
			input.baseOrigin, input.headOrigin = "environment", "environment"
		}, base: "head", head: "head", mode: "extra-ref-no-pulls"},
		{name: "When a no-pulls extra-ref declares a base, it should honor that base", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			extra := pullRefs(repo.base, "")
			extra.Workdir = true
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, extra)
		}, base: "base", head: "head", mode: "extra-ref-no-pulls"},
		{name: "When an extra-ref has pulls and exact revisions, it should preserve both", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			extra := pullRefs(repo.base, repo.head)
			extra.Workdir = true
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, extra)
			input.checkout = repo.main
		}, base: "base", head: "head", mode: "extra-ref-pulls"},
		{name: "When a command-line base overrides foreign environment, it should retain the extra-ref head", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			input.base, input.baseOrigin = repo.base, "command line"
			input.head, input.headOrigin = "foreign-head", "environment"
		}, base: "base", head: "head", mode: "extra-ref-no-pulls"},
		{name: "When a command-line head overrides foreign environment, it should retain the snapshot base", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			input.head, input.headOrigin = repo.base, "command line"
		}, base: "head", head: "base", mode: "extra-ref-no-pulls"},
		{name: "When GitHub Actions supplies exact revisions without JOB_SPEC, it should preserve them", modify: func(_ *testing.T, repo repositoryFixture, input *inputs) {
			input.base, input.head = repo.base, repo.main
			input.baseOrigin, input.headOrigin = "environment", "environment"
		}, base: "base", head: "main", mode: "local"},
		{name: "When local base is intentionally empty, it should preserve unfiltered lint and derive the real gitlint base", modify: func(_ *testing.T, _ repositoryFixture, input *inputs) { input.baseOrigin = "command line" }, head: "head", mode: "local"},
		{name: "When origin HEAD and main differ, it should prefer the origin HEAD merge base", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.baseOrigin = "command line"
			fixtureGit(t, repo.root, "update-ref", "refs/remotes/origin/HEAD", repo.head)
		}, head: "head", mode: "local", commitBase: "head"},
		{name: "When origin main and master differ, it should prefer the origin main merge base", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.baseOrigin = "command line"
			fixtureGit(t, repo.root, "update-ref", "refs/remotes/origin/master", repo.head)
		}, head: "head", mode: "local", commitBase: "base"},
		{name: "When only origin master exists, it should use its real merge base", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.baseOrigin = "command line"
			fixtureGit(t, repo.root, "update-ref", "-d", "refs/remotes/origin/main")
			fixtureGit(t, repo.root, "update-ref", "refs/remotes/origin/master", repo.head)
		}, head: "head", mode: "local", commitBase: "head"},
		{name: "When no local merge base exists, it should not invent a commit base", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.baseOrigin = "command line"
			fixtureGit(t, repo.root, "update-ref", "-d", "refs/remotes/origin/main")
		}, head: "head", mode: "local", noCommitBase: true},
		{name: "When explicit symbolic revisions are supplied, it should resolve them once", modify: func(_ *testing.T, _ repositoryFixture, input *inputs) {
			input.base, input.head = "main", "topic"
			input.baseOrigin, input.headOrigin = "command line", "command line"
		}, base: "main", head: "head", mode: "local"},
		{name: "When a primary PR omits pulls, it should reject missing declared head", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, pullRefs(repo.base, ""))
			input.checkout = repo.main
		}, err: "requires one declared pull head"},
		{name: "When primary pulls are empty, it should reject even a head override", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.jobSpec = `{"refs":{"org":"openshift","repo":"hypershift","base_sha":"` + repo.base + `","pulls":[]}}`
			input.head, input.headOrigin = repo.head, "command line"
		}, err: "requires one declared pull head"},
		{name: "When a supplied extra-ref base is malformed, it should fail", modify: func(t *testing.T, _ repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true, BaseSHA: "invalid"})
		}, err: "base_sha"},
		{name: "When an extra-ref pull base is unresolvable, it should fail", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			extra := pullRefs(strings.Repeat("f", 40), repo.head)
			extra.Workdir = true
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, extra)
		}, err: "base_sha"},
		{name: "When an extra-ref pull head is unresolvable, it should fail", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			extra := pullRefs(repo.base, strings.Repeat("f", 40))
			extra.Workdir = true
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, extra)
		}, err: "pull head"},
		{name: "When a foreign context has an empty explicit base, it should reject rather than disable comparison", modify: func(t *testing.T, _ repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			input.baseOrigin = "command line"
		}, err: "explicit base is empty"},
		{name: "When a foreign context has an invalid explicit head, it should reject rather than use the snapshot", modify: func(t *testing.T, _ repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			input.head, input.headOrigin = "missing", "command line"
		}, err: "commit head"},
		{name: "When a foreign context has an empty explicit head, it should fail", modify: func(t *testing.T, _ repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			input.headOrigin = "command line"
		}, err: "explicit head is empty"},
		{name: "When a foreign context has an invalid explicit base, it should fail", modify: func(t *testing.T, _ repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			input.base, input.baseOrigin = "missing", "command line"
		}, err: "comparison base"},
		{name: "When an extra-ref pull head is malformed, it should fail", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			extra := pullRefs(repo.base, "invalid")
			extra.Workdir = true
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, extra)
		}, err: "pull head"},
		{name: "When a primary PR omits its base, it should reject rather than use main", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, pullRefs("", repo.head))
		}, err: "requires an exact base_sha"},
		{name: "When an explicit base is invalid, it should fail without falling back", modify: func(_ *testing.T, _ repositoryFixture, input *inputs) {
			input.baseOrigin, input.base = "command line", "missing"
		}, err: "comparison base"},
		{name: "When an explicit head is invalid, it should fail without falling back", modify: func(_ *testing.T, _ repositoryFixture, input *inputs) {
			input.headOrigin, input.head = "command line", "missing"
		}, err: "commit head"},
		{name: "When an explicit head is empty, it should fail", modify: func(_ *testing.T, _ repositoryFixture, input *inputs) { input.headOrigin = "command line" }, err: "head is empty"},
		{name: "When the snapshot is unresolvable, it should fail", modify: func(_ *testing.T, _ repositoryFixture, input *inputs) { input.checkout = "missing" }, err: "checkout snapshot"},
		{name: "When the normal declared base is missing locally, it should fail", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, pullRefs(strings.Repeat("a", 40), repo.head))
		}, err: "base_sha"},
		{name: "When the normal declared head is invalid, it should fail", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			input.jobSpec = jobJSON(t, pullRefs(repo.base, "invalid"))
		}, err: "pull head"},
		{name: "When a pull-bearing extra-ref omits its exact base, it should fail even with an override", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			extra := pullRefs("", repo.head)
			extra.Workdir = true
			input.jobSpec = jobJSON(t, refs{Org: "openshift", Repo: "release"}, extra)
			input.base, input.baseOrigin = repo.base, "command line"
		}, err: "requires an exact base_sha"},
		{name: "When multiple pull heads are declared, it should fail as ambiguous", modify: func(t *testing.T, repo repositoryFixture, input *inputs) {
			primary := pullRefs(repo.base, repo.head)
			primary.Pulls = append(primary.Pulls, primary.Pulls[0])
			input.jobSpec = jobJSON(t, primary)
		}, err: "ambiguous"},
		{name: "When an existing tree is supplied as a base, it should reject the non-commit object", modify: func(t *testing.T, _ repositoryFixture, input *inputs) {
			input.base, input.baseOrigin = fixtureGit(t, input.root, "rev-parse", "HEAD^{tree}"), "command line"
		}, err: "comparison base"},
		{name: "When an existing blob is supplied as a base, it should reject the non-commit object", modify: func(t *testing.T, _ repositoryFixture, input *inputs) {
			input.base, input.baseOrigin = fixtureGit(t, input.root, "rev-parse", "HEAD:value.txt"), "command line"
		}, err: "comparison base"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			repo := newRepository(t)
			input := inputs{root: repo.root, checkout: repo.head, upstream: "upstream", baseOrigin: "undefined", headOrigin: "undefined"}
			if scenario.modify != nil {
				scenario.modify(t, repo, &input)
			}
			result, err := resolveContext(t.Context(), input)
			if scenario.err != "" {
				g.Expect(err).To(MatchError(ContainSubstring(scenario.err)))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			revisions := map[string]string{"": "", "base": repo.base, "head": repo.head, "main": repo.main}
			g.Expect(result.base).To(Equal(revisions[scenario.base]))
			g.Expect(result.head).To(Equal(revisions[scenario.head]))
			g.Expect(result.mode).To(Equal(scenario.mode))
			if result.base == "" {
				expected := repo.base
				if scenario.noCommitBase {
					expected = ""
				} else if scenario.commitBase != "" {
					expected = revisions[scenario.commitBase]
				}
				g.Expect(result.commitBase).To(Equal(expected))
			} else {
				g.Expect(result.commitBase).To(Equal(result.base))
			}
		})
	}
}

func TestSelectRefs(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, metadata, owner, repo, err string
	}{
		{name: "When JSON is malformed, it should fail", metadata: `{`, err: "malformed"},
		{name: "When primary refs are absent, it should fail", metadata: `{}`, err: "identity"},
		{name: "When primary refs are null, it should fail", metadata: `{"refs":null}`, err: "malformed"},
		{name: "When primary refs have the wrong type, it should fail", metadata: `{"refs":[]}`, err: "malformed"},
		{name: "When a foreign primary has no extra-ref, it should fail", metadata: `{"refs":{"org":"openshift","repo":"release"}}`, err: "unique"},
		{name: "When workdir is missing, it should fail", metadata: `{"refs":{"org":"openshift","repo":"release"},"extra_refs":[{"org":"openshift","repo":"hypershift"}]}`, err: "unique"},
		{name: "When matching extra-refs are duplicated, it should fail", metadata: `{"refs":{"org":"openshift","repo":"release"},"extra_refs":[{"org":"openshift","repo":"hypershift","workdir":true},{"org":"openshift","repo":"hypershift","workdir":true}]}`, err: "ambiguous"},
		{name: "When another repository claims the workdir, it should fail", metadata: `{"refs":{"org":"openshift","repo":"release"},"extra_refs":[{"org":"openshift","repo":"other","workdir":true}]}`, err: "conflicting"},
		{name: "When known fields are duplicated, it should fail", metadata: `{"refs":{},"refs":{}}`, err: "duplicate"},
		{name: "When workdir has the wrong type, it should fail", metadata: `{"refs":{"org":"openshift","repo":"release"},"extra_refs":[{"workdir":"true"}]}`, err: "malformed"},
		{name: "When pulls has the wrong type, it should fail", metadata: `{"refs":{"org":"openshift","repo":"hypershift","pulls":{}}}`, err: "malformed"},
		{name: "When base_sha has the wrong type, it should fail", metadata: `{"refs":{"org":"openshift","repo":"hypershift","base_sha":4}}`, err: "malformed"},
		{name: "When environment contradicts metadata, it should fail", metadata: `{"refs":{"org":"openshift","repo":"hypershift"}}`, owner: "openshift", repo: "release", err: "disagree"},
		{name: "When foreign environment lacks JOB_SPEC, it should fail", owner: "openshift", repo: "release", err: "requires JOB_SPEC"},
		{name: "When primary and extra-ref both claim workdir, it should fail", metadata: `{"refs":{"org":"openshift","repo":"release","workdir":true},"extra_refs":[{"org":"openshift","repo":"hypershift","workdir":true}]}`, err: "conflicting primary workdir"},
		{name: "When extra-ref identity is missing, it should fail", metadata: `{"refs":{"org":"openshift","repo":"release"},"extra_refs":[{}]}`, err: "identity"},
		{name: "When normal environment lacks JOB_SPEC, it should allow local inputs", owner: "openshift", repo: "hypershift"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := selectRefs(inputs{jobSpec: scenario.metadata, owner: scenario.owner, repo: scenario.repo})
			if scenario.err != "" {
				NewWithT(t).Expect(err).To(MatchError(ContainSubstring(scenario.err)))
			} else {
				NewWithT(t).Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestValidateJSON(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, value string
		valid       bool
	}{
		{name: "When nested metadata is valid, it should accept it", value: `{"refs":{"org":"openshift"},"extra_refs":[]}`, valid: true},
		{name: "When JSON has trailing data, it should fail", value: `{} {}`},
		{name: "When JSON has duplicate keys, it should fail", value: `{"refs":{"org":"a","org":"b"}}`},
		{name: "When JSON is incomplete, it should fail", value: `{"refs":`},
		{name: "When top-level refs has a case alias, it should fail", value: `{"refs":{},"Refs":{}}`},
		{name: "When repository identity has a case alias, it should fail", value: `{"refs":{"repo":"release","Repo":"hypershift"}}`},
		{name: "When base revision has a case alias, it should fail", value: `{"refs":{"base_sha":"a","Base_Sha":"b"}}`},
		{name: "When pull revision has a case alias, it should fail", value: `{"refs":{"pulls":[{"sha":"a","SHA":"b"}]}}`},
		{name: "When unrelated fields contain null, it should preserve Prow compatibility", value: `{"decoration_config":null}`, valid: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			err := validateJSON(json.NewDecoder(strings.NewReader(scenario.value)))
			NewWithT(t).Expect(err == nil).To(Equal(scenario.valid))
		})
	}
}

func TestResolveDeclaredContext(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name        string
		foreign     bool
		missingHead bool
	}{
		{name: "When primary metadata is complete, it should retain declared revisions"},
		{name: "When primary head is absent, it should fail before applying overrides", missingHead: true},
		{name: "When foreign workdir has no pulls or base, it should snapshot the checkout", foreign: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			repo := newRepository(t)
			primary := pullRefs(repo.base, repo.head)
			if scenario.missingHead {
				primary.Pulls = nil
			}
			metadata := jobJSON(t, primary)
			if scenario.foreign {
				metadata = jobJSON(t, refs{Org: "openshift", Repo: "release"}, refs{Org: "openshift", Repo: "hypershift", Workdir: true})
			}
			result, foreign, err := resolveDeclaredContext(t.Context(), inputs{root: repo.root, checkout: repo.main, jobSpec: metadata})
			if scenario.missingHead {
				NewWithT(t).Expect(err).To(MatchError(ContainSubstring("requires one declared pull head")))
				return
			}
			NewWithT(t).Expect(err).NotTo(HaveOccurred())
			NewWithT(t).Expect(foreign).To(Equal(scenario.foreign))
			if scenario.foreign {
				NewWithT(t).Expect(result.base).To(Equal(repo.main))
			} else {
				NewWithT(t).Expect(result.base).To(Equal(repo.base))
				NewWithT(t).Expect(result.head).To(Equal(repo.head))
			}
		})
	}
}

func TestResolveDeclaredCommit(t *testing.T) {
	t.Parallel()
	repo := newRepository(t)
	for _, revision := range []string{repo.base, "HEAD", strings.Repeat("f", 40), ""} {
		t.Run("When declared revision is "+emptyToken(revision)+", it should require an exact resolvable commit", func(t *testing.T) {
			t.Parallel()
			result, err := resolveDeclaredCommit(t.Context(), repo.root, revision)
			if revision == repo.base {
				NewWithT(t).Expect(err).NotTo(HaveOccurred())
				NewWithT(t).Expect(result).To(Equal(repo.base))
			} else {
				NewWithT(t).Expect(err).To(HaveOccurred())
			}
		})
	}
}

func TestResolveCommit(t *testing.T) {
	t.Parallel()
	repo := newRepository(t)
	for _, revision := range []string{"HEAD", "missing", ""} {
		t.Run("When local revision is "+emptyToken(revision)+", it should resolve only commit references", func(t *testing.T) {
			t.Parallel()
			result, err := resolveCommit(t.Context(), repo.root, revision)
			if revision == "HEAD" {
				NewWithT(t).Expect(err).NotTo(HaveOccurred())
				NewWithT(t).Expect(result).To(Equal(repo.head))
			} else {
				NewWithT(t).Expect(err).To(HaveOccurred())
			}
		})
	}
}

func TestGit(t *testing.T) {
	t.Parallel()
	repo := newRepository(t)
	result, err := git(t.Context(), repo.root, "rev-parse", "HEAD")
	NewWithT(t).Expect(err).NotTo(HaveOccurred())
	NewWithT(t).Expect(result).To(Equal(repo.head))
	_, err = git(t.Context(), repo.root, "not-a-git-command")
	NewWithT(t).Expect(err).To(HaveOccurred())
}

func TestEmptyToken(t *testing.T) {
	t.Parallel()
	NewWithT(t).Expect(emptyToken("")).To(Equal("-"))
	NewWithT(t).Expect(emptyToken("value")).To(Equal("value"))
}

func TestRun(t *testing.T) {
	t.Parallel()
	repo := newRepository(t)
	var output bytes.Buffer
	err := run(t.Context(), []string{"--repo-root", repo.root, "--checkout-head", repo.head, "--base", repo.base, "--base-origin", "command line"}, &output, inputs{})
	NewWithT(t).Expect(err).NotTo(HaveOccurred())
	NewWithT(t).Expect(strings.Fields(output.String())).To(Equal([]string{repo.base, repo.head, "local", repo.head, repo.base}))
	output.Reset()
	err = run(t.Context(), []string{"--unknown"}, &output, inputs{})
	NewWithT(t).Expect(err).To(HaveOccurred())
	NewWithT(t).Expect(output.Len()).To(BeZero())
}
