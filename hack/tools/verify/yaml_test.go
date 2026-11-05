package verify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestNormaliseCEL(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"When empty, it should stay empty", "", ""},
		{"When there are no guards, it should preserve conditions", `event == "pull_request"`, `event == "pull_request"`},
		{"When a wildcard guard is positive, it should remove it", `event == "push" && ".tekton/***".pathChanged()`, `event == "push"`},
		{"When a wildcard guard is negated, it should remove it", `event == "push" && !".tekton/***".pathChanged()`, `event == "push"`},
		{"When specific guards are negated, it should remove them", `event == "push" && !".tekton/foo.yaml".pathChanged() && !".tekton/bar.yaml".pathChanged()`, `event == "push"`},
		{"When a guard group spans lines, it should remove it", "event == \"push\"\n && (\".tekton/a.yaml\".pathChanged()\n || \".tekton/b.yaml\".pathChanged())", `event == "push"`},
		{"When a trigger contains nested calls, it should match parentheses", `event == "push" && (files.all.exists(x, !x.matches('^docs/')) || "Containerfile".pathChanged())`, `event == "push"`},
		{"When a string contains a parenthesis, it should ignore it", `event == "push" && (files.all.exists(x, x.matches('a)b')) || "Dockerfile".pathChanged())`, `event == "push"`},
		{"When a string contains an escaped quote, it should match the group", `event == "push" && (files.all.exists(x, x.matches('a\')b')) || "Dockerfile".pathChanged())`, `event == "push"`},
		{"When a group is unrelated to paths, it should preserve it", `event == "push" && (target_branch == "main")`, `event == "push" && (target_branch == "main")`},
		{"When a group is unbalanced, it should preserve it", `event == "push" && ("foo".pathChanged()`, `event == "push" && ("foo".pathChanged()`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normaliseCEL(tc.input); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
func pipelineFixture() (map[string]any, map[string]any) {
	orig := map[string]any{"metadata": map[string]any{"name": "comp-on-pr", "namespace": "crt-redhat-acm-tenant", "labels": map[string]any{"app": "hypershift"}, "annotations": map[string]any{pipelineAnnotation: commonPipeline, celAnnotation: `event == "pull_request" && target_branch == "main" && (".tekton/pipelines/common-operator-build.yaml".pathChanged() || ".tekton/comp-pull-request.yaml".pathChanged())`}}, "spec": map[string]any{"params": []any{map[string]any{"name": "revision", "value": "{{revision}}"}}, "pipelineRef": map[string]any{"name": "hypershift-common-operator-build"}}}
	data, _ := json.Marshal(orig)
	var fm map[string]any
	_ = json.Unmarshal(data, &fm)
	meta := object(fm["metadata"])
	meta["name"] = "comp-on-pr-from-main"
	a := object(meta["annotations"])
	delete(a, pipelineAnnotation)
	a[celAnnotation] = `event == "pull_request" && target_branch == "main" && ("src/***".pathChanged()) && !".tekton/pipelines/common-operator-build.yaml".pathChanged() && !".tekton/comp-pull-request.yaml".pathChanged()`
	params := []any{}
	for _, key := range []string{"url", "revision", "pathInRepo"} {
		params = append(params, map[string]any{"name": key, "value": resolverParams[key]})
	}
	object(fm["spec"])["pipelineRef"] = map[string]any{"resolver": "git", "params": params}
	return orig, fm
}
func TestCheckPair(t *testing.T) {
	for _, tc := range []struct {
		name, issue string
		change      func(map[string]any, map[string]any)
	}{
		{"When differences are expected, it should accept them", "", func(o, f map[string]any) {}},
		{"When PR annotation is missing, it should reject it", "missing pipeline", func(o, f map[string]any) { delete(object(object(o["metadata"])["annotations"]), pipelineAnnotation) }},
		{"When from-main has a local pipeline, it should reject it", "must NOT", func(o, f map[string]any) {
			object(object(f["metadata"])["annotations"])[pipelineAnnotation] = commonPipeline
		}},
		{"When a positive guard is missing, it should reject it", "CEL missing pipeline", func(o, f map[string]any) {
			object(object(o["metadata"])["annotations"])[celAnnotation] = `event == "pull_request"`
		}},
		{"When negated guards are missing, it should reject it", "CEL missing negated", func(o, f map[string]any) {
			object(object(f["metadata"])["annotations"])[celAnnotation] = `event == "pull_request"`
		}},
		{"When base conditions differ, it should reject them", "CEL mismatch", func(o, f map[string]any) {
			a := object(object(f["metadata"])["annotations"])
			a[celAnnotation] = strings.ReplaceAll(text(a[celAnnotation]), `target_branch == "main"`, `target_branch == "release-4.22"`)
		}},
		{"When another annotation changes, it should reject it", "annotation mismatch", func(o, f map[string]any) { object(object(f["metadata"])["annotations"])["extra"] = "value" }},
		{"When labels drift, it should reject it", "label mismatch", func(o, f map[string]any) { object(object(f["metadata"])["labels"])["extra"] = "value" }},
		{"When namespaces differ, it should reject it", "namespace mismatch", func(o, f map[string]any) { object(f["metadata"])["namespace"] = "other" }},
		{"When spec changes, it should reject it", "spec mismatch", func(o, f map[string]any) { object(f["spec"])["timeout"] = "1h" }},
		{"When resolver is not git, it should reject it", "must be git", func(o, f map[string]any) { object(object(f["spec"])["pipelineRef"])["resolver"] = "bundles" }},
		{"When revision changes, it should reject it", "param revision", func(o, f map[string]any) {
			list(object(object(f["spec"])["pipelineRef"])["params"])[1].(map[string]any)["value"] = "release-4.22"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, f := pipelineFixture()
			tc.change(o, f)
			err := checkPair(o, f, "comp-pull-request.yaml")
			if tc.issue == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.issue) {
				t.Fatalf("want %q, got %v", tc.issue, err)
			}
		})
	}
}
func TestRunner_TektonPairs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   []string
		wantErr bool
	}{
		{"When pairs exist, it should validate them", []string{"comp-pull-request.yaml", "comp-pull-request-from-main.yaml"}, false},
		{"When PR is orphaned, it should fail", []string{"comp-pull-request.yaml"}, true},
		{"When from-main is orphaned, it should fail", []string{"comp-pull-request-from-main.yaml"}, true},
		{"When there are no pairs, it should fail", []string{"comp-push.yaml"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{ctx: context.Background(), root: t.TempDir()}
			dir := filepath.Join(r.root, ".tekton")
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			for _, name := range tc.files {
				o, f := pipelineFixture()
				doc := o
				if strings.Contains(name, "from-main") {
					doc = f
				}
				data, err := yaml.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.tektonPairs(); (err != nil) != tc.wantErr {
				t.Fatalf("got %v", err)
			}
		})
	}
}
func TestRunner_NavOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []any
		wantErr bool
	}{
		{"When index is first, it should accept sorted entries", []any{"index.md", map[string]any{"Alpha": "a.md"}, map[string]any{"beta": "b.md"}}, false},
		{"When index is last, it should reject it", []any{map[string]any{"Alpha": "a.md"}, "index.md"}, true},
		{"When entries are unordered, it should reject them", []any{map[string]any{"Zebra": "z.md"}, map[string]any{"Alpha": "a.md"}}, true},
		{"When subsections are unordered, it should leave them unchecked", []any{map[string]any{"Group": []any{map[string]any{"Z": "z.md"}, map[string]any{"A": "a.md"}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &runner{root: t.TempDir()}
			if err := r.navOrder(tc.entries, "How-to guides"); (err != nil) != tc.wantErr {
				t.Fatalf("got %v", err)
			}
		})
	}
}
