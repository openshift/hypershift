package verify

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

var spellingSkip = []string{"./docs/site/*", "./vendor/*", "./api/vendor/*", "./hack/tools/vendor/*", "./api/hypershift/v1alpha1/*", "./support/thirdparty/*", "./docs/content/reference/*", "./hack/tools/bin/*", "./cmd/install/assets/*", "./go.sum", "./api/go.sum", "./hack/workspace/go.work.sum", "./api/hypershift/v1beta1/zz_generated.featuregated-crd-manifests", "./hack/tools/go.mod", "./hack/tools/go.sum", "./karpenter-operator/controllers/karpenter/assets/*.yaml", "./dev/*", "./pkg/capi/*", "./hack/capi-vendor/*", "./hack/tools/verify/spelling/*"}
var spellingWord = regexp.MustCompile(`[\pL\pN_\-'’]+`)
var spellingIgnore = regexp.MustCompile(`[^\pL\pN_\s]\s?codespell:ignore\b(?:\s+([\pL\pN_,]*))?`)

func (r *runner) spelling() error {
	ignoreData, err := os.ReadFile(filepath.Join(r.root, ".codespellignore"))
	if err != nil {
		return err
	}
	ignore := map[string]bool{}
	for word := range strings.SplitSeq(string(ignoreData), "\n") {
		word = strings.TrimSpace(word)
		if word != "" && !strings.HasPrefix(word, "#") {
			ignore[word] = true
		}
	}
	dictionary := map[string]string{}
	for _, name := range []string{"dictionary.txt", "dictionary_rare.txt"} {
		data, err := os.ReadFile(filepath.Join(r.root, "hack/tools/verify/spelling", name))
		if err != nil {
			return err
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if line == "" {
				continue
			}
			key, value, ok := strings.Cut(line, "->")
			if !ok {
				return fmt.Errorf("malformed dictionary line %q", line)
			}
			dictionary[strings.ToLower(key)] = strings.ToLower(strings.TrimSpace(value))
			if strings.Contains(key, "'") {
				dictionary[strings.ReplaceAll(strings.ToLower(key), "'", "’")] = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "'", "’")
			}
		}
	}
	var errs []error
	err = filepath.WalkDir(r.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == r.root {
			return nil
		}
		rel, err := filepath.Rel(r.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// Codespell's default recursive traversal excludes hidden paths and symlinks.
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		skip := false
		for _, pattern := range spellingSkip {
			if pythonGlob(pattern, "./"+rel) {
				skip = true
				break
			}
		}
		if skip {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, message := range spellingLine(line, dictionary, ignore) {
				errs = append(errs, fmt.Errorf("%s:%d: %s", rel, i+1, message))
			}
		}
		return nil
	})
	return errors.Join(append(errs, err)...)
}
func spellingLine(line string, dictionary map[string]string, ignore map[string]bool) []string {
	inline := map[string]bool{}
	if match := spellingIgnore.FindStringSubmatch(line); match != nil {
		if match[1] == "" {
			return nil
		}
		for _, word := range strings.Split(match[1], ",") {
			inline[word] = true
		}
	}
	var issues []string
	for _, loc := range spellingWord.FindAllStringIndex(line, -1) {
		word := line[loc[0]:loc[1]]
		lower := strings.ToLower(word)
		suggestion, ok := dictionary[lower]
		if !ok || ignore[word] || ignore[lower] || inline[lower] {
			continue
		}
		if loc[0] > 0 && line[loc[0]-1] == '\\' && strings.ContainsRune("abfnrtv", rune(word[0])) {
			if _, bad := dictionary[lower[1:]]; !bad {
				continue
			}
		}
		issues = append(issues, word+" ==> "+suggestion)
	}
	return issues
}

// Unlike filepath.Match, Python fnmatch's '*' spans directory separators.
func pythonGlob(pattern, path string) bool {
	var re strings.Builder
	re.WriteByte('^')
	for _, c := range pattern {
		switch c {
		case '*':
			re.WriteString(".*")
		case '?':
			re.WriteByte('.')
		default:
			re.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	re.WriteByte('$')
	return regexp.MustCompile(re.String()).MatchString(path)
}
