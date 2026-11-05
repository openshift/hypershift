package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCheckCommit(t *testing.T) {
	policy := map[string]string{"title-max-length.line-length": "120", "body-max-line-length.line-length": "140", "contrib-title-conventional-commits.types": "fix,feat,chore,docs,style,refactor,perf,test,revert,ci,build"}
	valid := "fix(tooling): preserve native checks\n\nKeep all existing validation behavior.\n"
	for _, tc := range []struct{ name, msg, email, issue string }{
		{"When a commit is conventional, it should accept it", valid, "bcox@redhat.com", ""},
		{"When the title is too long, it should reject it", "fix: " + strings.Repeat("a", 120) + "\n\nAn explanatory commit body.", "", "T1"},
		{"When body lines are too long, it should reject them", "fix: native checks\n\n" + strings.Repeat("a", 141), "", "B1"},
		{"When the body is missing, it should reject it", "fix: native checks", "", "B6"},
		{"When the body is short, it should reject it", "fix: native checks\n\nshort", "", "B5"},
		{"When the second line is not blank, it should reject it", "fix: native checks\nAn explanatory commit body.", "", "B4"},
		{"When the title has punctuation, it should reject it", strings.Replace(valid, "checks\n", "checks.\n", 1), "", "T3"},
		{"When the type is unknown, it should reject it", strings.Replace(valid, "fix(", "unknown(", 1), "", "CT1"},
		{"When whitespace trails a line, it should reject it", strings.Replace(valid, "behavior.", "behavior. ", 1), "", "B2"},
		{"When a line has a hard tab, it should reject it", strings.Replace(valid, "existing validation", "existing\tvalidation", 1), "", "B3"},
		{"When the title is WIP, it should reject it", strings.Replace(valid, "preserve", "WIP", 1), "", "T5"},
		{"When the author email is invalid, it should reject it", valid, "invalid", "M1"},
		{"When the commit is a fixup, it should ignore it", "fixup! existing commit", "", ""},
		{"When the commit is a revert, it should ignore it", `Revert "original change"`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCommit(tc.msg, tc.email, policy)
			if tc.issue == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.issue) {
				t.Fatalf("want %s, got %v", tc.issue, err)
			}
		})
	}
}
func TestSpellingLine(t *testing.T) {
	dictionary := map[string]string{"teh": "the", "wrod": "word", "nd": "and", "can'tt": "can't", "can’tt": "can’t"} // codespell:ignore
	for _, tc := range []struct {
		name, line string
		ignore     map[string]bool
		count      int
	}{
		{"When words are misspelled, it should report each occurrence", "teh wrod", nil, 2},                    // codespell:ignore
		{"When the spelling is uppercase, it should still reject it", "TEH", nil, 1},                           // codespell:ignore
		{"When a lowercase ignore exists, it should ignore all cases", "TEH", map[string]bool{"teh": true}, 0}, // codespell:ignore
		{"When a cased ignore differs, it should keep checking", "TEH", map[string]bool{"Teh": true}, 1},       // codespell:ignore
		/* codespell:ignore */ {"When an inline ignore names a word, it should ignore that word", "teh wrod # codespell:ignore teh", nil, 1},
		{"When inline ignore has no words, it should skip the line", "teh # codespell:ignore", nil, 0},
		{"When a backslash escape prefixes a valid word, it should ignore it", `\nd`, nil, 0},
		{"When apostrophes are curly, it should check them", "can’tt", nil, 1}, // codespell:ignore
		{"When a typo is part of an identifier, it should leave it alone", "teh_identifier", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := spellingLine(tc.line, dictionary, tc.ignore)
			if len(got) != tc.count {
				t.Fatalf("got %v, want %d issues", got, tc.count)
			}
		})
	}
}
func TestRunner_Containerfiles(t *testing.T) {
	for _, tc := range []struct {
		name, other string
		bad         bool
	}{
		{"When only the base differs, it should accept it", "FROM different\nRUN go build\nCOPY . .\n", false},
		{"When source copy differs, it should accept it", "FROM base\nRUN go build\nCOPY --from=source . .\n", false},
		{"When build commands differ, it should reject it", "FROM base\nRUN go test\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{ctx: context.Background(), root: t.TempDir()}
			for name, data := range map[string]string{"Containerfile.control-plane": "FROM base\nRUN go build\nCOPY . .\n", "Dockerfile.control-plane": tc.other} {
				if err := os.WriteFile(filepath.Join(r.root, name), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.containerfiles(); (err != nil) != tc.bad {
				t.Fatalf("got %v", err)
			}
		})
	}
}
func TestEnvironment(t *testing.T) {
	got := environment([]string{"CGO_ENABLED=1", "PATH=/bin", "GOFLAGS=-race"}, "CGO_ENABLED=0", "GOFLAGS=-mod=vendor")
	want := []string{"PATH=/bin", "CGO_ENABLED=0", "GOFLAGS=-mod=vendor"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRunner_Perform(t *testing.T) {
	for _, tc := range []struct {
		name, task     string
		failRepository bool
		failLeaf       string
		wantPhases     []string
	}{
		{"When repository verification runs, it should exclude analysis", "verify-repository", false, "", []string{"verify-repository"}},
		{"When lint runs, it should exclude repository verification", "lint", false, "", []string{"lint"}},
		{"When full verification runs, it should include both CI phases", "verify", false, "", []string{"verify-repository", "lint", "verify"}},
		{"When repository verification fails, it should stop full verification", "verify", true, "", nil},
		{"When staticcheck fails, it should fail lint", "lint", false, "staticcheck", nil},
		{"When vet fails, it should fail lint", "lint", false, "vet", nil},
		{"When module analysis fails, it should fail lint", "lint", false, "lint-modules", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VERIFY_JOBS", "2")
			root := t.TempDir()
			for _, args := range [][]string{{"init", "--quiet"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "--allow-empty", "-m", "fixture"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = root
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("prepare Git fixture: %v: %s", err, out)
				}
			}
			// Cache expensive leaves so the real orchestration can run in an empty
			// checkout. An accidental dependency on the other phase fails there.
			done := map[string]bool{}
			for _, name := range []string{"update", "generate", "fmt", "capi-sync", "verify-capi-decoupling", "verify-tool-tests", "verify-codespell", "verify-codecov", "verify-api-deps", "verify-crd-schema", "cpo-container-sync", "run-gitlint", "verify-docs-nav", "verify-tekton-pipeline-pairs", "staticcheck", "vet", "lint-modules", "verify-git-clean"} {
				done[name] = true
			}
			if tc.task == "verify-repository" {
				for _, name := range []string{"staticcheck", "vet", "lint-modules"} {
					delete(done, name)
				}
			} else if tc.task == "lint" {
				delete(done, "update")
				delete(done, "verify-git-clean")
			}
			if tc.failRepository {
				delete(done, "verify-git-clean")
				if err := os.WriteFile(filepath.Join(root, "uncommitted"), []byte("generated changes\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			delete(done, tc.failLeaf)
			r := &runner{ctx: context.Background(), root: root, done: done}
			if err := r.task(tc.task); (err != nil) != (tc.failRepository || tc.failLeaf != "") {
				t.Fatalf("unexpected verification result: %v", err)
			}
			for _, phase := range []string{"verify-repository", "lint", "verify"} {
				want := false
				for _, name := range tc.wantPhases {
					want = want || phase == name
				}
				if r.done[phase] != want {
					t.Errorf("phase %s completed = %t, want %t", phase, r.done[phase], want)
				}
			}
		})
	}
}
