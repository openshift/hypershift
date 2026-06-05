package scraper

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ComplexityResult holds the delta in cyclomatic and cognitive complexity
// between the base branch and the PR head.
type ComplexityResult struct {
	CyclomaticDelta float64
	CognitiveDelta  float64
}

// ComplexityAnalyzer analyzes Go code complexity using gocyclo and gocognit.
type ComplexityAnalyzer struct {
	workDir string
}

// NewComplexityAnalyzer creates a ComplexityAnalyzer that will clone repos
// into subdirectories of workDir.
func NewComplexityAnalyzer(workDir string) *ComplexityAnalyzer {
	return &ComplexityAnalyzer{
		workDir: workDir,
	}
}

// AnalyzePR clones the repository, checks out both base and head branches,
// runs gocyclo and gocognit on each, and computes the delta.
// If the tools are not available, it returns zero deltas without failing.
func (a *ComplexityAnalyzer) AnalyzePR(ctx context.Context, owner, repo string, prNumber int, baseBranch string) (*ComplexityResult, error) {
	// Check if tools are available
	if !isToolAvailable("gocyclo") || !isToolAvailable("gocognit") {
		// Log warning and return zero deltas instead of failing
		fmt.Fprintf(os.Stderr, "Warning: gocyclo or gocognit not found, returning zero complexity deltas\n")
		return &ComplexityResult{
			CyclomaticDelta: 0,
			CognitiveDelta:  0,
		}, nil
	}

	// Create temp directory for cloning
	cloneDir := filepath.Join(a.workDir, fmt.Sprintf("%s-%s-%d", owner, repo, prNumber))
	if err := os.MkdirAll(cloneDir, 0755); err != nil {
		return nil, fmt.Errorf("creating clone directory: %w", err)
	}
	defer os.RemoveAll(cloneDir)

	// Clone repo (shallow clone for efficiency)
	repoURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	cloneCmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--no-single-branch", repoURL, cloneDir)
	if output, err := cloneCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("cloning repo: %w (output: %s)", err, output)
	}

	// Analyze base branch
	baseCyclomatic, baseCognitive, err := a.analyzeRef(ctx, cloneDir, baseBranch)
	if err != nil {
		return nil, fmt.Errorf("analyzing base branch %s: %w", baseBranch, err)
	}

	// Analyze PR head (fetch PR ref and checkout)
	prRef := fmt.Sprintf("pull/%d/head", prNumber)
	fetchCmd := exec.CommandContext(ctx, "git", "-C", cloneDir, "fetch", "origin", prRef)
	if output, err := fetchCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("fetching PR ref: %w (output: %s)", err, output)
	}

	headCyclomatic, headCognitive, err := a.analyzeRef(ctx, cloneDir, "FETCH_HEAD")
	if err != nil {
		return nil, fmt.Errorf("analyzing PR head: %w", err)
	}

	return &ComplexityResult{
		CyclomaticDelta: computeComplexityDelta(baseCyclomatic, headCyclomatic),
		CognitiveDelta:  computeComplexityDelta(baseCognitive, headCognitive),
	}, nil
}

// analyzeRef checks out the given ref and runs gocyclo and gocognit.
func (a *ComplexityAnalyzer) analyzeRef(ctx context.Context, repoDir, ref string) (cyclomatic, cognitive float64, err error) {
	// Checkout ref
	checkoutCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", ref)
	if output, err := checkoutCmd.CombinedOutput(); err != nil {
		return 0, 0, fmt.Errorf("checking out %s: %w (output: %s)", ref, err, output)
	}

	// Run gocyclo -avg .
	cycloCmd := exec.CommandContext(ctx, "gocyclo", "-avg", ".")
	cycloCmd.Dir = repoDir
	cycloOutput, err := cycloCmd.CombinedOutput()
	if err != nil {
		return 0, 0, fmt.Errorf("running gocyclo: %w (output: %s)", err, cycloOutput)
	}

	cyclomatic, err = ParseGocycloOutput(string(cycloOutput))
	if err != nil {
		return 0, 0, fmt.Errorf("parsing gocyclo output: %w", err)
	}

	// Run gocognit -avg .
	cognitCmd := exec.CommandContext(ctx, "gocognit", "-avg", ".")
	cognitCmd.Dir = repoDir
	cognitOutput, err := cognitCmd.CombinedOutput()
	if err != nil {
		return 0, 0, fmt.Errorf("running gocognit: %w (output: %s)", err, cognitOutput)
	}

	cognitive, err = ParseGocognitOutput(string(cognitOutput))
	if err != nil {
		return 0, 0, fmt.Errorf("parsing gocognit output: %w", err)
	}

	return cyclomatic, cognitive, nil
}

// gocycloRe matches the "Average: X.Y (over N functions)" line from gocyclo -avg
var gocycloRe = regexp.MustCompile(`Average:\s+([0-9.]+)\s+\(over\s+\d+\s+functions?\)`)

// ParseGocycloOutput extracts the average complexity from gocyclo -avg output.
// Expected format: "Average: 3.45 (over 100 functions)"
func ParseGocycloOutput(output string) (float64, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return 0, fmt.Errorf("empty gocyclo output")
	}

	matches := gocycloRe.FindStringSubmatch(output)
	if len(matches) < 2 {
		return 0, fmt.Errorf("failed to parse gocyclo output: %q", output)
	}

	avg, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing average value %q: %w", matches[1], err)
	}

	return avg, nil
}

// gocognitRe matches the "Average: X.Y (over N functions)" line from gocognit -avg
var gocognitRe = regexp.MustCompile(`Average:\s+([0-9.]+)\s+\(over\s+\d+\s+functions?\)`)

// ParseGocognitOutput extracts the average complexity from gocognit -avg output.
// Expected format: "Average: 7.82 (over 150 functions)"
func ParseGocognitOutput(output string) (float64, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return 0, fmt.Errorf("empty gocognit output")
	}

	matches := gocognitRe.FindStringSubmatch(output)
	if len(matches) < 2 {
		return 0, fmt.Errorf("failed to parse gocognit output: %q", output)
	}

	avg, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing average value %q: %w", matches[1], err)
	}

	return avg, nil
}

// computeComplexityDelta calculates the difference between head and base complexity.
func computeComplexityDelta(base, head float64) float64 {
	return head - base
}

// isToolAvailable checks if a command-line tool is available in the PATH.
func isToolAvailable(tool string) bool {
	_, err := exec.LookPath(tool)
	return err == nil
}
