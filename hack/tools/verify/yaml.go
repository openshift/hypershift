package verify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func loadYAML(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if doc == nil {
		return nil, fmt.Errorf("%s: empty YAML document", path)
	}
	return doc, nil
}
func object(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
func text(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
func list(value any) []any {
	if s, ok := value.([]any); ok {
		return s
	}
	return nil
}

func (r *runner) docsNav() error {
	data, err := os.ReadFile(filepath.Join(r.root, "docs/mkdocs.yml"))
	if err != nil {
		return err
	}
	// Inspect only nav nodes: Python-specific MkDocs extension tags elsewhere
	// in the config are configuration data, never code to execute.
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		return errors.New("empty MkDocs configuration")
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "nav" {
			continue
		}
		var nav []any
		if err := root.Content[i+1].Decode(&nav); err != nil {
			return err
		}
		for _, entry := range nav {
			if section, ok := object(entry)["How-to guides"]; ok {
				return r.navOrder(list(section), "How-to guides")
			}
		}
	}
	return errors.New("could not find How-to guides in MkDocs nav")
}
func (r *runner) navOrder(entries []any, section string) error {
	var titles []string
	var errs []error
	seenRegular := false
	for i, entry := range entries {
		title, path := text(entry), text(entry)
		if values, ok := entry.(map[string]any); ok {
			if len(values) != 1 {
				return fmt.Errorf("%s: nav entry must have exactly one key", section)
			}
			for key, value := range values {
				title = key
				path = text(value)
			}
		} else {
			title = r.markdownTitle(path)
		}
		name := filepath.Base(path)
		index := name == "index.md" || name == "_index.md" || strings.HasSuffix(name, "-index.md")
		if index {
			if seenRegular {
				errs = append(errs, fmt.Errorf("%s: index entry must appear first (position %d)", section, i+1))
			}
			continue
		}
		seenRegular = true
		titles = append(titles, title)
	}
	sorted := slices.Clone(titles)
	fold := cases.Fold()
	slices.SortStableFunc(sorted, func(a, b string) int { return strings.Compare(fold.String(a), fold.String(b)) })
	if !slices.Equal(titles, sorted) {
		errs = append(errs, fmt.Errorf("%s is not alphabetical: got %q, want %q", section, titles, sorted))
	}
	return errors.Join(errs...)
}
func (r *runner) markdownTitle(path string) string {
	data, err := os.ReadFile(filepath.Join(r.root, "docs/content", path))
	if err == nil {
		content := string(data)
		if strings.HasPrefix(content, "---") {
			if end := strings.Index(content[3:], "---"); end >= 0 {
				for line := range strings.SplitSeq(content[3:3+end], "\n") {
					if title, ok := strings.CutPrefix(strings.TrimSpace(line), "title:"); ok {
						return strings.Trim(strings.TrimSpace(title), "\"'")
					}
				}
			}
		}
		for line := range strings.SplitSeq(content, "\n") {
			if title, ok := strings.CutPrefix(line, "# "); ok {
				return strings.TrimSpace(title)
			}
		}
	}
	return cases.Title(language.Und).String(strings.ReplaceAll(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "-", " "))
}

const pipelineAnnotation = "pipelinesascode.tekton.dev/pipeline"
const celAnnotation = "pipelinesascode.tekton.dev/on-cel-expression"
const commonPipeline = ".tekton/pipelines/common-operator-build.yaml"

var resolverParams = map[string]string{"url": "https://github.com/openshift/hypershift.git", "revision": "main", "pathInRepo": commonPipeline}

func (r *runner) tektonPairs() error {
	dir := filepath.Join(r.root, ".tekton")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	pairs := 0
	for _, entry := range entries {
		name := entry.Name()
		var counterpart string
		switch {
		case strings.HasSuffix(name, "-pull-request-from-main.yaml"):
			counterpart = strings.ReplaceAll(name, "-pull-request-from-main.yaml", "-pull-request.yaml")
			if _, err := os.Stat(filepath.Join(dir, counterpart)); err != nil {
				errs = append(errs, fmt.Errorf("%s: orphaned from-main pipeline", name))
			}
		case strings.HasSuffix(name, "-pull-request.yaml"):
			counterpart = strings.ReplaceAll(name, "-pull-request.yaml", "-pull-request-from-main.yaml")
			orig, err := loadYAML(filepath.Join(dir, name))
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(dir, counterpart)); os.IsNotExist(err) {
				if _, ok := object(object(orig["metadata"])["annotations"])[pipelineAnnotation]; ok {
					errs = append(errs, fmt.Errorf("%s: local pipeline has no from-main counterpart", name))
				}
				continue
			}
			fm, err := loadYAML(filepath.Join(dir, counterpart))
			if err != nil {
				return err
			}
			pairs++
			if err := checkPair(orig, fm, name); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
		}
	}
	if pairs == 0 {
		errs = append(errs, errors.New("no PipelineRun pairs found"))
	}
	return errors.Join(errs...)
}
func checkPair(orig, fm map[string]any, name string) error {
	var errs []error
	check := func(ok bool, message string) {
		if !ok {
			errs = append(errs, errors.New(message))
		}
	}
	om, fmMeta := object(orig["metadata"]), object(fm["metadata"])
	oa, fa := object(om["annotations"]), object(fmMeta["annotations"])
	_, hasOrig := oa[pipelineAnnotation]
	_, hasFM := fa[pipelineAnnotation]
	check(hasOrig, "PR file is missing pipeline annotation")
	check(!hasFM, "from-main file must NOT have pipeline annotation")
	oc, fc := text(oa[celAnnotation]), text(fa[celAnnotation])
	for _, path := range []string{commonPipeline, ".tekton/" + name} {
		guard := `"` + path + `".pathChanged()`
		check(strings.Contains(oc, guard), "CEL missing pipeline guard "+guard)
		check(strings.Contains(fc, "!"+guard), "CEL missing negated guard !"+guard)
	}
	check(normaliseCEL(oc) == normaliseCEL(fc), "normalised CEL mismatch")
	for _, key := range []string{pipelineAnnotation, celAnnotation} {
		delete(oa, key)
		delete(fa, key)
	}
	check(reflect.DeepEqual(oa, fa), "annotation mismatch")
	check(reflect.DeepEqual(object(om["labels"]), object(fmMeta["labels"])), "label mismatch")
	check(reflect.DeepEqual(om["namespace"], fmMeta["namespace"]), "namespace mismatch")
	ospec, fspec := object(orig["spec"]), object(fm["spec"])
	ref := object(fspec["pipelineRef"])
	delete(ospec, "pipelineRef")
	delete(fspec, "pipelineRef")
	check(reflect.DeepEqual(ospec, fspec), "spec mismatch (excluding pipelineRef)")
	check(text(ref["resolver"]) == "git", "pipelineRef.resolver must be git")
	params := map[string]any{}
	for _, item := range list(ref["params"]) {
		p := object(item)
		params[text(p["name"])] = p["value"]
	}
	for key, value := range resolverParams {
		check(params[key] == value, "pipelineRef param "+key+" must equal "+value)
	}
	return errors.Join(errs...)
}
func normaliseCEL(input string) string {
	for _, pattern := range []string{`\s*&&\s*!?"\s*\.tekton/\*\*\*\s*"\.pathChanged\(\)\s*`, `\s*&&\s*\(\s*"\.tekton/[^"]+\.yaml"\.pathChanged\(\)\s*\|\|\s*"\.tekton/[^"]+\.yaml"\.pathChanged\(\)\s*\)`, `\s*&&\s*!"\.tekton/[^"]+\.yaml"\.pathChanged\(\)`} {
		input = regexp.MustCompile(pattern).ReplaceAllString(input, " ")
	}
	var out strings.Builder
	for i := 0; i < len(input); {
		if strings.HasPrefix(input[i:], "&&") {
			j := i + 2
			for j < len(input) && unicode.IsSpace(rune(input[j])) {
				j++
			}
			if j < len(input) && input[j] == '(' {
				end := balancedParen(input, j)
				if end > j {
					block := input[j:end]
					if strings.Contains(block, ".pathChanged()") || strings.Contains(block, "files.all.exists") {
						out.WriteByte(' ')
						i = end
						continue
					}
				}
			}
		}
		out.WriteByte(input[i])
		i++
	}
	return strings.Join(strings.Fields(out.String()), " ")
}
func balancedParen(input string, start int) int {
	depth := 1
	var quote byte
	for i := start + 1; i < len(input); i++ {
		c := input[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
