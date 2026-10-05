// deadcode-report runs whole-program analysis and publishes first-party candidates.
package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
)

const (
	scanTags        = "integration,e2e,reqserving,e2ev2,backuprestore"
	scanEnvironment = "GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOPACKAGESDRIVER=off"
)

var reportNames = []string{"deadcode.json", "deadcode.txt", "deadcode-summary.txt"}

type scanMetadata struct {
	commit, dirty, toolVersion, goVersion, memoryLimit, gc string
	elapsed                                                time.Duration
}

func main() {
	tool := flag.String("deadcode", "hack/tools/bin/deadcode", "path to a trusted, locally built pinned deadcode executable")
	artifacts := flag.String("artifact-dir", "/tmp/artifacts", "output directory for the three reports")
	flag.Parse()
	if err := run(context.Background(), ".", *tool, *artifacts); err != nil {
		fmt.Fprintf(os.Stderr, "deadcode-report: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, root, tool, artifacts string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("locating repository: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolving repository: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("reading root module: %w", err)
	}
	module, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return fmt.Errorf("parsing root go.mod: %w", err)
	}
	if module.Module == nil {
		return fmt.Errorf("invalid root go.mod: module declaration is required")
	}
	tool, err = filepath.Abs(tool)
	if err != nil {
		return fmt.Errorf("locating analyzer: %w", err)
	}
	if err := os.MkdirAll(artifacts, 0755); err != nil {
		return fmt.Errorf("creating artifact directory: %w", err)
	}
	// Invalidate previous reports before starting a new scan in this directory.
	for i := len(reportNames) - 1; i >= 0; i-- {
		name := reportNames[i]
		path := filepath.Join(artifacts, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("checking old report %s: %w", name, err)
		}
		if info.IsDir() {
			return fmt.Errorf("report destination %s is a directory", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing old report %s: %w", name, err)
		}
	}
	version, err := analyzerVersion(tool)
	if err != nil {
		return err
	}
	metadata := scanMetadata{toolVersion: version, memoryLimit: "6GiB", gc: "50"}
	if value := os.Getenv("GOMEMLIMIT"); value != "" {
		metadata.memoryLimit = value
	}
	if value := os.Getenv("GOGC"); value != "" {
		metadata.gc = value
	}
	env := append(os.Environ(), strings.Fields(scanEnvironment)...)
	env = append(env, "GOMEMLIMIT="+metadata.memoryLimit, "GOGC="+metadata.gc)
	commit, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return fmt.Errorf("determining analyzed commit: %w: %s", err, commit)
	}
	metadata.commit = strings.TrimSpace(string(commit))
	dirty, err := exec.CommandContext(ctx, "git", "-C", root, "diff", "--quiet", "HEAD", "--").CombinedOutput()
	metadata.dirty = "false"
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
			return fmt.Errorf("determining tracked dirty state: %w: %s", err, dirty)
		}
		metadata.dirty = "true"
	}
	goCommand := exec.CommandContext(ctx, "go", "env", "GOVERSION")
	goCommand.Dir, goCommand.Env = root, env
	goVersion, err := goCommand.CombinedOutput()
	if err != nil {
		return fmt.Errorf("determining analysis Go version: %w: %s", err, goVersion)
	}
	metadata.goVersion = strings.TrimSpace(string(goVersion))
	filter := "^" + regexp.QuoteMeta(module.Module.Mod.Path) + "(/|_test$|$)"
	// The caller-selected executable and its filesystem are trusted local inputs.
	// analyzerVersion checks compatibility, not authenticity. Execute the path
	// directly with separate arguments, never through a shell (see DEVELOPMENT.md).
	cmd := exec.CommandContext(ctx, tool, "-json", "-generated", "-test", "-filter="+filter, "-tags="+scanTags, "./...")
	cmd.Dir, cmd.Env = root, env
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	started := time.Now()
	output, err := cmd.Output()
	metadata.elapsed = time.Since(started)
	if err != nil {
		return fmt.Errorf("analysis failed: %w\n%s", err, diagnostics.String())
	}
	if diagnostics.Len() > 0 {
		fmt.Fprint(os.Stderr, diagnostics.String())
	}
	findings, totals, err := readFindings(output, root, module.Module.Mod.Path)
	if err != nil {
		return err
	}
	if err := writeReports(artifacts, findings, totals, metadata); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "deadcode-report: completed analysis; %d candidates in %s\n", len(findings), artifacts)
	return nil
}

func analyzerVersion(tool string) (string, error) {
	info, err := buildinfo.ReadFile(tool)
	if err != nil {
		return "", fmt.Errorf("reading analyzer build information: %w", err)
	}
	if info.Path != "golang.org/x/tools/cmd/deadcode" || info.Main.Path != "golang.org/x/tools" || info.Main.Replace != nil || info.Main.Version != "v0.44.0" {
		return "", fmt.Errorf("analyzer must be golang.org/x/tools/cmd/deadcode v0.44.0, got %s %+v", info.Path, info.Main)
	}
	return info.Main.Version, nil
}
