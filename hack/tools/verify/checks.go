package verify

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func (r *runner) containerfiles() error {
	clean := func(file string) ([]byte, error) {
		data, err := os.ReadFile(filepath.Join(r.root, file))
		if err != nil {
			return nil, err
		}
		var out []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "FROM ") || regexp.MustCompile(`COPY .* \. \.`).MatchString(line) || regexp.MustCompile(`COPY \. \.`).MatchString(line) {
				continue
			}
			out = append(out, line)
		}
		return []byte(strings.Join(out, "\n")), nil
	}
	a, err := clean("Containerfile.control-plane")
	if err != nil {
		return err
	}
	b, err := clean("Dockerfile.control-plane")
	if err != nil {
		return err
	}
	if !bytes.Equal(a, b) {
		return errors.New("Containerfile.control-plane and Dockerfile.control-plane differ beyond their base image and source-copy instructions")
	}
	return nil
}
func (r *runner) codecov() error {
	data, err := os.ReadFile(filepath.Join(r.root, "codecov.yml"))
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext}}
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-r.ctx.Done():
				return r.ctx.Err()
			case <-time.After(time.Second):
			}
		}
		request, err := http.NewRequestWithContext(r.ctx, http.MethodPost, "https://codecov.io/validate", bytes.NewReader(data))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := client.Do(request)
		if err != nil {
			last = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if readErr == nil && response.StatusCode >= 200 && response.StatusCode < 300 && regexp.MustCompile(`(?m)^Valid!`).Match(body) {
			return nil
		}
		last = fmt.Errorf("CodeCov HTTP %d: %s: %v", response.StatusCode, body, readErr)
	}
	return last
}

// commitPolicy reads the same repository settings used by gitlint. Unknown
// settings fail explicitly so a future policy change cannot silently be ignored.
func (r *runner) commitPolicy() (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(r.root, ".gitlint"))
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	section := ""
	allowed := map[string]bool{"general.contrib": true, "contrib-title-conventional-commits.types": true, "title-max-length.line-length": true, "body-max-line-length.line-length": true}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = section + "." + strings.TrimSpace(key)
		if !ok || !allowed[key] {
			return nil, fmt.Errorf("unsupported .gitlint setting %q; update the Go commit checker", line)
		}
		values[key] = strings.TrimSpace(value)
	}
	if values["general.contrib"] != "contrib-title-conventional-commits" {
		return nil, errors.New("unsupported gitlint contrib rules")
	}
	return values, nil
}

func (r *runner) commits() error {
	base, err := r.comparisonBase()
	if err != nil {
		return err
	}
	head := os.Getenv("PULL_PULL_SHA")
	if head == "" {
		head = "HEAD"
	}
	out, err := r.output("", nil, "git", "rev-list", "--no-merges", base+".."+head)
	if err != nil {
		return err
	}
	policy, err := r.commitPolicy()
	if err != nil {
		return err
	}
	var errs []error
	for sha := range strings.FieldsSeq(string(out)) {
		metadata, err := r.output("", nil, "git", "show", "-s", "--format=%ae%x00%B", sha)
		if err != nil {
			return err
		}
		email, msg, ok := strings.Cut(string(metadata), "\x00")
		if !ok {
			return errors.New("invalid git show output")
		}
		if err := checkCommit(msg, email, policy); err != nil {
			errs = append(errs, fmt.Errorf("commit %s: %w", sha, err))
		}
	}
	return errors.Join(errs...)
}
func (r *runner) pendingCommit() error {
	file := os.Getenv("COMMIT_MSG_FILE")
	if file == "" {
		return errors.New("COMMIT_MSG_FILE must be set")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	policy, err := r.commitPolicy()
	if err != nil {
		return err
	}
	return checkCommit(string(data), "", policy)
}

func checkCommit(message, email string, policy map[string]string) error {
	// Gitlint ignores squash/fixup/revert commits by default, and strips comment
	// lines from messages supplied by a commit-msg hook.
	lines := strings.Split(strings.TrimRight(message, "\n"), "\n")
	lines = slices.DeleteFunc(lines, func(line string) bool { return strings.HasPrefix(line, "#") })
	if len(lines) == 0 {
		return errors.New("empty commit message")
	}
	title := lines[0]
	if strings.HasPrefix(title, "fixup!") || strings.HasPrefix(title, "squash!") || strings.HasPrefix(title, "Revert") || strings.HasPrefix(title, "amend!") || strings.HasPrefix(title, "Merge") {
		return nil
	}
	titleMax, err := strconv.Atoi(policy["title-max-length.line-length"])
	if err != nil {
		return err
	}
	bodyMax, err := strconv.Atoi(policy["body-max-line-length.line-length"])
	if err != nil {
		return err
	}
	var errs []error
	add := func(rule string, line int, text string) {
		errs = append(errs, fmt.Errorf("%d: %s %s", line, rule, text))
	}
	if n := utf8.RuneCountInString(title); n > titleMax {
		add("T1", 1, fmt.Sprintf("title exceeds %d characters", titleMax))
	}
	if utf8.RuneCountInString(title) < 5 {
		add("T8", 1, "title is too short")
	}
	if strings.TrimRightFunc(title, unicode.IsSpace) != title {
		add("T2", 1, "trailing whitespace")
	}
	if strings.TrimLeftFunc(title, unicode.IsSpace) != title {
		add("T6", 1, "leading whitespace")
	}
	if strings.ContainsAny(title, "\t") {
		add("T4", 1, "hard tab")
	}
	if strings.HasSuffix(title, "?") || strings.HasSuffix(title, ":") || strings.HasSuffix(title, "!") || strings.HasSuffix(title, ".") || strings.HasSuffix(title, ",") || strings.HasSuffix(title, ";") {
		add("T3", 1, "trailing punctuation")
	}
	if regexp.MustCompile(`(?i)\bWIP\b`).MatchString(title) {
		add("T5", 1, "title contains WIP")
	}
	match := regexp.MustCompile(`^([^(]+?)(\([^)]+?\))?!?: .+`).FindStringSubmatch(title)
	types := strings.Split(strings.ReplaceAll(policy["contrib-title-conventional-commits.types"], " ", ""), ",")
	if match == nil || !slices.Contains(types, match[1]) {
		add("CT1", 1, "use type(optional-scope): description with an allowed type")
	}
	if len(lines) > 1 && lines[1] != "" {
		add("B4", 2, "second line must be empty")
	}
	body := strings.Join(lines[1:], "")
	if strings.TrimSpace(body) == "" {
		add("B6", 3, "body is missing")
	} else if utf8.RuneCountInString(body) < 20 {
		add("B5", 3, "body is shorter than 20 characters")
	}
	for i, line := range lines[1:] {
		if utf8.RuneCountInString(line) > bodyMax {
			add("B1", i+2, fmt.Sprintf("line exceeds %d characters", bodyMax))
		}
		if strings.TrimRightFunc(line, unicode.IsSpace) != line {
			add("B2", i+2, "trailing whitespace")
		}
		if strings.Contains(line, "\t") {
			add("B3", i+2, "hard tab")
		}
	}
	if email != "" && !regexp.MustCompile(`^[^@ ]+@[^@ ]+\.[^@ ]+`).MatchString(email) {
		add("M1", 1, "invalid author email")
	}
	return errors.Join(errs...)
}
