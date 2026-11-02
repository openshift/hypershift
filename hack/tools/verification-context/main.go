package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type inputs struct {
	root, checkout, base, head, baseOrigin, headOrigin, upstream string
	jobSpec, owner, repo                                         string
}

type verificationContext struct {
	base, head, mode, checkout, commitBase string
}

type refs struct {
	Org     string `json:"org"`
	Repo    string `json:"repo"`
	BaseSHA string `json:"base_sha,omitempty"`
	Workdir bool   `json:"workdir,omitempty"`
	Pulls   []struct {
		SHA string `json:"sha"`
	} `json:"pulls,omitempty"`
}

func main() {
	input := inputs{jobSpec: os.Getenv("JOB_SPEC"), owner: os.Getenv("REPO_OWNER"), repo: os.Getenv("REPO_NAME")}
	if err := run(context.Background(), os.Args[1:], os.Stdout, input); err != nil {
		fmt.Fprintf(os.Stderr, "verification context: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer, input inputs) error {
	flags := flag.NewFlagSet("verification-context", flag.ContinueOnError)
	flags.StringVar(&input.root, "repo-root", ".", "checkout root")
	flags.StringVar(&input.checkout, "checkout-head", "HEAD", "pre-generation checkout revision")
	flags.StringVar(&input.base, "base", "", "original comparison base")
	flags.StringVar(&input.head, "head", "", "original commit range head")
	flags.StringVar(&input.baseOrigin, "base-origin", "undefined", "Make input origin")
	flags.StringVar(&input.headOrigin, "head-origin", "undefined", "Make input origin")
	flags.StringVar(&input.upstream, "upstream-remote", "", "local upstream remote name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	resolved, err := resolveContext(ctx, input)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "%s %s %s %s %s\n", emptyToken(resolved.base), resolved.head, resolved.mode, resolved.checkout, emptyToken(resolved.commitBase))
	return err
}

func emptyToken(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func resolveContext(ctx context.Context, input inputs) (verificationContext, error) {
	result, foreign, err := resolveDeclaredContext(ctx, input)
	if err != nil {
		return result, err
	}
	if !foreign || input.baseOrigin == "command line" {
		if input.baseOrigin != "undefined" {
			if input.base == "" && result.mode != "local" {
				return result, fmt.Errorf("explicit base is empty in %s context", result.mode)
			}
			result.base = input.base
		}
	}
	if !foreign || input.headOrigin == "command line" {
		if input.headOrigin != "undefined" {
			if input.head == "" {
				return result, fmt.Errorf("explicit head is empty")
			}
			result.head = input.head
		}
	}
	if result.base == "" && input.baseOrigin == "undefined" {
		result.base = "main"
		if input.upstream != "" {
			result.base = input.upstream + "/main"
		}
	}
	if result.head == "" {
		result.head = result.checkout
	}
	if result.base != "" {
		result.base, err = resolveCommit(ctx, input.root, result.base)
		if err != nil {
			return result, fmt.Errorf("comparison base: %w", err)
		}
	}
	result.head, err = resolveCommit(ctx, input.root, result.head)
	if err != nil {
		return result, fmt.Errorf("commit head: %w", err)
	}
	result.commitBase = result.base
	if result.mode == "local" && result.base == "" {
		for _, candidate := range []string{"origin/HEAD", "origin/main", "origin/master"} {
			base, gitErr := git(ctx, input.root, "merge-base", result.head, candidate)
			if gitErr == nil {
				result.commitBase = base
				break
			}
		}
	}
	return result, nil
}

func resolveDeclaredContext(ctx context.Context, input inputs) (verificationContext, bool, error) {
	result := verificationContext{mode: "local"}
	selected, foreign, err := selectRefs(input)
	if err != nil {
		return result, foreign, err
	}
	result.checkout, err = resolveCommit(ctx, input.root, input.checkout)
	if err != nil {
		return result, foreign, fmt.Errorf("checkout snapshot: %w", err)
	}
	if selected == nil {
		return result, foreign, nil
	}
	result.mode = "primary"
	if foreign {
		result.mode = "extra-ref-no-pulls"
		if len(selected.Pulls) != 0 {
			result.mode = "extra-ref-pulls"
		}
	}
	if !foreign && len(selected.Pulls) == 0 {
		return result, foreign, fmt.Errorf("primary context requires one declared pull head")
	}
	if len(selected.Pulls) > 1 {
		return result, foreign, fmt.Errorf("%s has ambiguous multiple pull heads", result.mode)
	}
	if selected.BaseSHA == "" {
		if !foreign || len(selected.Pulls) != 0 {
			return result, foreign, fmt.Errorf("%s requires an exact base_sha", result.mode)
		}
		result.base = result.checkout
	} else {
		result.base, err = resolveDeclaredCommit(ctx, input.root, selected.BaseSHA)
		if err != nil {
			return result, foreign, fmt.Errorf("%s base_sha: %w", result.mode, err)
		}
	}
	if len(selected.Pulls) == 1 {
		result.head, err = resolveDeclaredCommit(ctx, input.root, selected.Pulls[0].SHA)
		if err != nil {
			return result, foreign, fmt.Errorf("%s pull head: %w", result.mode, err)
		}
	}
	return result, foreign, nil
}

func selectRefs(input inputs) (*refs, bool, error) {
	if input.jobSpec == "" {
		if (input.owner != "" || input.repo != "") && (input.owner != "openshift" || input.repo != "hypershift") {
			return nil, false, fmt.Errorf("foreign primary repository requires JOB_SPEC workdir context")
		}
		return nil, false, nil
	}
	if err := validateJSON(json.NewDecoder(strings.NewReader(input.jobSpec))); err != nil {
		return nil, false, fmt.Errorf("malformed JOB_SPEC: %w", err)
	}
	var job struct {
		Refs      *refs  `json:"refs"`
		ExtraRefs []refs `json:"extra_refs"`
	}
	if err := json.Unmarshal([]byte(input.jobSpec), &job); err != nil {
		return nil, false, fmt.Errorf("malformed JOB_SPEC fields: %w", err)
	}
	if job.Refs == nil || job.Refs.Org == "" || job.Refs.Repo == "" {
		return nil, false, fmt.Errorf("JOB_SPEC requires primary repository identity")
	}
	if (input.owner != "" && input.owner != job.Refs.Org) || (input.repo != "" && input.repo != job.Refs.Repo) {
		return nil, false, fmt.Errorf("JOB_SPEC and primary repository environment disagree")
	}
	primaryMatches := job.Refs.Org == "openshift" && job.Refs.Repo == "hypershift"
	if !primaryMatches && job.Refs.Workdir {
		return nil, true, fmt.Errorf("conflicting primary workdir repository context")
	}
	var selected *refs
	for index := range job.ExtraRefs {
		extra := &job.ExtraRefs[index]
		if extra.Org == "" || extra.Repo == "" {
			return nil, !primaryMatches, fmt.Errorf("extra_ref requires repository identity")
		}
		matches := extra.Org == "openshift" && extra.Repo == "hypershift"
		if extra.Workdir && (!matches || primaryMatches) {
			return nil, false, fmt.Errorf("conflicting workdir repository context")
		}
		if matches {
			if selected != nil {
				return nil, false, fmt.Errorf("ambiguous HyperShift extra_refs")
			}
			selected = extra
		}
	}
	if primaryMatches {
		return job.Refs, false, nil
	}
	if selected == nil || !selected.Workdir {
		return nil, true, fmt.Errorf("foreign primary requires a unique HyperShift workdir extra_ref")
	}
	return selected, true, nil
}

func validateJSON(decoder *json.Decoder) error {
	var walk func(bool) error
	walk = func(required bool) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if token == nil && required {
			return fmt.Errorf("null metadata is not supported")
		}
		delimiter, isDelimiter := token.(json.Delim)
		if !isDelimiter {
			return nil
		}
		seen := map[string]bool{}
		for decoder.More() {
			required := false
			if delimiter == '{' {
				key, keyErr := decoder.Token()
				if keyErr != nil {
					return keyErr
				}
				name := key.(string)
				if seen[name] {
					return fmt.Errorf("duplicate field %q", name)
				}
				seen[name] = true
				switch strings.ToLower(name) {
				case "refs", "extra_refs", "org", "repo", "base_sha", "workdir", "pulls", "sha":
					if name != strings.ToLower(name) {
						return fmt.Errorf("noncanonical context field %q", name)
					}
					required = true
				}
			}
			if err := walk(required); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	if err := walk(true); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}

func resolveDeclaredCommit(ctx context.Context, root, revision string) (string, error) {
	decoded, err := hex.DecodeString(revision)
	if err != nil || len(decoded) != 20 {
		return "", fmt.Errorf("expected a full commit SHA")
	}
	return resolveCommit(ctx, root, revision)
}

func resolveCommit(ctx context.Context, root, revision string) (string, error) {
	if revision == "" {
		return "", fmt.Errorf("missing revision")
	}
	return git(ctx, root, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
}

func git(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
