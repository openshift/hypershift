package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gobwas/glob"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"sigs.k8s.io/yaml"
)

// pipelineRun is a minimal representation to extract the CEL annotation and name.
type pipelineRun struct {
	Metadata struct {
		Name        string            `json:"name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

func setupPager() func() {
	if !isTerminal(os.Stdout) {
		return func() {}
	}
	pager := os.Getenv("PAGER")
	if pager == "" {
		// Use less with options similar to systemctl/git:
		// -R: pass through ANSI escape sequences (for future color support)
		// -F: quit if output fits on one screen
		// -X: don't clear screen on exit
		// -S: chop long lines instead of wrapping
		pager = "less"
		os.Setenv("LESS", "RFXS")
	}

	r, w, err := os.Pipe()
	if err != nil {
		return func() {}
	}

	cmd := exec.Command(pager)
	cmd.Stdin = r
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	origStdout := os.Stdout
	os.Stdout = w

	if err := cmd.Start(); err != nil {
		os.Stdout = origStdout
		w.Close()
		r.Close()
		return func() {}
	}

	return func() {
		w.Close()
		cmd.Wait()
		os.Stdout = origStdout
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func main() {
	cleanup := setupPager()
	defer cleanup()

	baseRef := "origin/main"
	if len(os.Args) > 1 {
		baseRef = os.Args[1]
	}

	changedFiles, err := getChangedFiles(baseRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting changed files: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Changed files (%d):\n", len(changedFiles))
	for _, f := range changedFiles {
		fmt.Printf("  %s\n", f)
	}
	fmt.Println()

	repoRoot, err := getRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding repo root: %v\n", err)
		os.Exit(1)
	}
	tektonDir := filepath.Join(repoRoot, ".tekton")
	entries, err := os.ReadDir(tektonDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", tektonDir, err)
		os.Exit(1)
	}

	type result struct {
		name    string
		status  string
		celExpr string
	}

	var results []result
	hasFailure := false
	maxName := len("Pipeline")

	for _, entry := range entries {
		if !strings.Contains(entry.Name(), "pull-request") || entry.IsDir() {
			continue
		}

		path := filepath.Join(tektonDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
			continue
		}

		var pr pipelineRun
		if err := yaml.Unmarshal(data, &pr); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", path, err)
			continue
		}

		celExpr, ok := pr.Metadata.Annotations["pipelinesascode.tekton.dev/on-cel-expression"]
		if !ok {
			r := result{name: pr.Metadata.Name, status: "SKIP", celExpr: "(no CEL expression)"}
			results = append(results, r)
			if len(r.name) > maxName {
				maxName = len(r.name)
			}
			continue
		}

		celExpr = strings.Join(strings.Fields(celExpr), " ")

		triggered, err := evaluateCEL(celExpr, changedFiles)
		if err != nil {
			r := result{name: pr.Metadata.Name, status: "ERROR", celExpr: err.Error()}
			results = append(results, r)
			hasFailure = true
			if len(r.name) > maxName {
				maxName = len(r.name)
			}
			continue
		}

		status := "no"
		if triggered {
			status = "YES"
		}
		r := result{name: pr.Metadata.Name, status: status, celExpr: celExpr}
		results = append(results, r)
		if len(r.name) > maxName {
			maxName = len(r.name)
		}
	}

	fmt.Println("Pipeline trigger evaluation (event=pull_request, target_branch=main):")
	fmt.Println()

	// Table header
	fmt.Printf("  %-*s  %-8s  %s\n", maxName, "Pipeline", "Trigger", "CEL Expression")
	fmt.Printf("  %s  %s  %s\n", strings.Repeat("─", maxName), strings.Repeat("─", 8), strings.Repeat("─", 40))

	for _, r := range results {
		marker := "  "
		if r.status == "YES" {
			marker = "▶ "
		}
		fmt.Printf("%s%-*s  %-8s  %s\n", marker, maxName, r.name, r.status, r.celExpr)
	}

	if hasFailure {
		os.Exit(1)
	}
}

func getRepoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func getChangedFiles(baseRef string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", baseRef+"...HEAD")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// celPac implements cel.Library, mirroring the Pipelines as Code CEL
// environment from tektoncd/pipelines-as-code/pkg/matcher/cel.go.
type celPac struct {
	changedFiles []string
}

func (c celPac) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("pathChanged",
			cel.MemberOverload("string_pathChanged",
				[]*cel.Type{cel.StringType},
				cel.BoolType,
				cel.UnaryBinding(c.pathChanged),
			),
		),
	}
}

func (c celPac) ProgramOptions() []cel.ProgramOption {
	return nil
}

// pathChanged matches exactly like PaC: uses gobwas/glob on each changed file.
func (c celPac) pathChanged(val ref.Val) ref.Val {
	pattern, ok := val.Value().(string)
	if !ok {
		return types.False
	}
	g, err := glob.Compile(pattern)
	if err != nil {
		return types.NewErr("glob compile error for %q: %v", pattern, err)
	}
	for _, f := range c.changedFiles {
		if g.Match(f) {
			return types.True
		}
	}
	return types.False
}

func evaluateCEL(expr string, changedFiles []string) (bool, error) {
	env, err := cel.NewEnv(
		cel.Lib(celPac{changedFiles: changedFiles}),
		cel.Variable("event", cel.StringType),
		cel.Variable("event_type", cel.StringType),
		cel.Variable("event_title", cel.StringType),
		cel.Variable("target_branch", cel.StringType),
		cel.Variable("source_branch", cel.StringType),
		cel.Variable("target_url", cel.StringType),
		cel.Variable("source_url", cel.StringType),
		cel.Variable("headers", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("body", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("files", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return false, fmt.Errorf("creating CEL env: %w", err)
	}

	ast, issues := env.Parse(expr)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("parsing CEL: %w", issues.Err())
	}

	checked, issues := env.Check(ast)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("checking CEL: %w", issues.Err())
	}

	prg, err := env.Program(checked)
	if err != nil {
		return false, fmt.Errorf("creating CEL program: %w", err)
	}

	// Build the files map matching PaC's structure
	allFiles := make([]interface{}, len(changedFiles))
	for i, f := range changedFiles {
		allFiles[i] = f
	}
	filesMap := map[string]interface{}{
		"all":      allFiles,
		"added":    []interface{}{},
		"deleted":  []interface{}{},
		"modified": allFiles, // approximate: treat all as modified
		"renamed":  []interface{}{},
	}

	out, _, err := prg.Eval(map[string]interface{}{
		"event":         "pull_request",
		"event_type":    "",
		"event_title":   "",
		"target_branch": "main",
		"source_branch": "",
		"target_url":    "",
		"source_url":    "",
		"headers":       map[string]interface{}{},
		"body":          map[string]interface{}{},
		"files":         filesMap,
	})
	if err != nil {
		return false, fmt.Errorf("evaluating CEL: %w", err)
	}

	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL result is not bool: %v (%T)", out.Value(), out.Value())
	}
	return result, nil
}
