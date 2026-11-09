package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// hyperShiftRepo identifies HyperShift refs by repository name so that
// private mirrors of the repository in other organizations are recognized.
const hyperShiftRepo = "hypershift"

// periodicJobType is the Prow job type that declares no primary refs.
const periodicJobType = "periodic"

// Verification modes report which declared refs selected the revisions.
const (
	modeLocal           = "local"
	modePrimary         = "primary"
	modePrimaryBatch    = "primary-batch"
	modeExtraRefNoPulls = "extra-ref-no-pulls"
	modeExtraRefPulls   = "extra-ref-pulls"
)

// Make $(origin) values that change override precedence.
const (
	originUndefined   = "undefined"
	originCommandLine = "command line"
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
		return fmt.Errorf("unexpected positional arguments: %q", flags.Args())
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
	if !foreign || input.baseOrigin == originCommandLine {
		if input.baseOrigin != originUndefined {
			if input.base == "" && result.mode != modeLocal {
				return result, fmt.Errorf("explicit base is empty in %s context", result.mode)
			}
			result.base = input.base
		}
	}
	if !foreign || input.headOrigin == originCommandLine {
		if input.headOrigin != originUndefined {
			if input.head == "" {
				return result, errors.New("explicit head is empty")
			}
			result.head = input.head
		}
	}
	if result.base == "" && input.baseOrigin == originUndefined {
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
	if result.mode == modeLocal && result.base == "" {
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
	result := verificationContext{mode: modeLocal}
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
	pulls := len(selected.Pulls)
	switch {
	case !foreign && pulls == 0:
		return result, foreign, errors.New("primary context requires a declared pull head")
	case !foreign && pulls == 1:
		result.mode = modePrimary
	case !foreign:
		// Tide batches merge every declared pull into the checkout, so the
		// merged checkout snapshot is the head of the tested change.
		result.mode = modePrimaryBatch
	case pulls == 0:
		result.mode = modeExtraRefNoPulls
	case pulls == 1:
		result.mode = modeExtraRefPulls
	default:
		return result, foreign, fmt.Errorf("%s has ambiguous multiple pull heads", modeExtraRefPulls)
	}
	if selected.BaseSHA == "" {
		if pulls != 0 {
			return result, foreign, fmt.Errorf("%s requires an exact base_sha", result.mode)
		}
		result.base = result.checkout
	} else {
		result.base, err = resolveDeclaredCommit(ctx, input.root, selected.BaseSHA)
		if err != nil {
			return result, foreign, fmt.Errorf("%s base_sha: %w", result.mode, err)
		}
	}
	var head string
	for _, pull := range selected.Pulls {
		if head, err = resolveDeclaredCommit(ctx, input.root, pull.SHA); err != nil {
			return result, foreign, fmt.Errorf("%s pull head: %w", result.mode, err)
		}
	}
	if pulls == 1 {
		result.head = head
	}
	return result, foreign, nil
}

// selectRefs returns the declared refs that describe the HyperShift checkout,
// and whether they are a workdir extra_ref of another primary repository.
// It returns nil refs when the job declares no pull request context.
func selectRefs(input inputs) (*refs, bool, error) {
	if input.jobSpec == "" {
		if (input.owner != "" || input.repo != "") && input.repo != hyperShiftRepo {
			return nil, false, errors.New("foreign primary repository requires JOB_SPEC workdir context")
		}
		return nil, false, nil
	}
	if err := validateJSON(json.NewDecoder(strings.NewReader(input.jobSpec))); err != nil {
		return nil, false, fmt.Errorf("malformed JOB_SPEC: %w", err)
	}
	var job struct {
		Type      string `json:"type"`
		Refs      *refs  `json:"refs"`
		ExtraRefs []refs `json:"extra_refs"`
	}
	if err := json.Unmarshal([]byte(input.jobSpec), &job); err != nil {
		return nil, false, fmt.Errorf("malformed JOB_SPEC fields: %w", err)
	}
	if job.Refs == nil && job.Type == periodicJobType && input.owner == "" && input.repo == "" {
		// Periodic jobs declare no primary refs, and Prow sets no REPO_* or
		// PULL_* variables for them, so local defaults and explicit overrides
		// apply exactly as they do without JOB_SPEC.
		return nil, false, nil
	}
	if job.Refs == nil || job.Refs.Org == "" || job.Refs.Repo == "" {
		return nil, false, errors.New("JOB_SPEC requires primary repository identity")
	}
	if (input.owner != "" && input.owner != job.Refs.Org) || (input.repo != "" && input.repo != job.Refs.Repo) {
		return nil, false, errors.New("JOB_SPEC and primary repository environment disagree")
	}
	var workdir *refs
	for index := range job.ExtraRefs {
		extra := &job.ExtraRefs[index]
		if extra.Org == "" || extra.Repo == "" {
			return nil, false, errors.New("extra_ref requires repository identity")
		}
		if extra.Workdir {
			if workdir != nil {
				return nil, false, errors.New("ambiguous workdir extra_refs")
			}
			workdir = extra
		}
	}
	primaryIsHyperShift := job.Refs.Repo == hyperShiftRepo
	if workdir == nil {
		if !primaryIsHyperShift {
			return nil, true, errors.New("foreign primary requires a unique HyperShift workdir extra_ref")
		}
		return job.Refs, false, nil
	}
	if job.Refs.Workdir {
		return nil, true, errors.New("conflicting primary workdir repository context")
	}
	if primaryIsHyperShift || workdir.Repo != hyperShiftRepo {
		return nil, false, errors.New("conflicting workdir repository context")
	}
	return workdir, true, nil
}

func validateJSON(decoder *json.Decoder) error {
	var walk func(bool) error
	walk = func(required bool) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if token == nil && required {
			return errors.New("null metadata is not supported")
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
				case "type", "refs", "extra_refs", "org", "repo", "base_sha", "workdir", "pulls", "sha":
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
		return errors.New("expected one JSON object")
	}
	return nil
}

func resolveDeclaredCommit(ctx context.Context, root, revision string) (string, error) {
	decoded, err := hex.DecodeString(revision)
	if err != nil || len(decoded) != 20 {
		return "", errors.New("expected a full commit SHA")
	}
	return resolveCommit(ctx, root, revision)
}

func resolveCommit(ctx context.Context, root, revision string) (string, error) {
	if revision == "" {
		return "", errors.New("missing revision")
	}
	return git(ctx, root, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
}

// git returns standard output only, so warnings that git prints to standard
// error on success cannot be mistaken for revisions. Those warnings are
// forwarded to standard error, which Make's $(shell) leaves visible.
func git(ctx context.Context, root string, args ...string) (string, error) {
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	if stderr.Len() != 0 {
		_, _ = os.Stderr.Write(stderr.Bytes())
	}
	return strings.TrimSpace(string(output)), nil
}
