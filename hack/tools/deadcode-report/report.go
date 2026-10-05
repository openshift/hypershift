package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type finding struct {
	Package  string `json:"package"`
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// These fields follow the JSON protocol of the pinned deadcode command.
type analyzerPackage struct {
	Name  string
	Path  string
	Funcs []analyzerFunction
}

type analyzerFunction struct {
	Name     string
	Position struct {
		File string
		Line int
		Col  int
	}
	Generated *bool
	Marker    *bool
}

type scanTotals struct {
	candidates, generated, vendor, copied, external, duplicates int
}

func readFindings(data []byte, root, modulePath string) ([]finding, scanTotals, error) {
	var packages []analyzerPackage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&packages); err != nil {
		return nil, scanTotals{}, fmt.Errorf("decoding deadcode JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, scanTotals{}, fmt.Errorf("deadcode JSON must contain exactly one value")
	} else if !errors.Is(err, io.EOF) {
		return nil, scanTotals{}, fmt.Errorf("decoding trailing deadcode JSON: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, scanTotals{}, fmt.Errorf("resolving source root: %w", err)
	}
	findings := []finding{}
	var totals scanTotals
	for _, pkg := range packages {
		if pkg.Name == "" || pkg.Path == "" || len(pkg.Funcs) == 0 {
			return nil, totals, fmt.Errorf("invalid deadcode package record: name, path and functions are required")
		}
		for _, fn := range pkg.Funcs {
			if fn.Name == "" || fn.Position.File == "" || fn.Position.Line < 1 || fn.Position.Col < 1 || fn.Generated == nil || fn.Marker == nil {
				return nil, totals, fmt.Errorf("invalid deadcode function record in %s: name, position and flags are required", pkg.Path)
			}
			totals.candidates++
			if pkg.Path != modulePath && pkg.Path != modulePath+"_test" && !strings.HasPrefix(pkg.Path, modulePath+"/") {
				totals.external++
				continue
			}
			file, err := sourcePath(canonicalRoot, fn.Position.File)
			if err != nil {
				return nil, totals, err
			}
			switch {
			case file == "":
				totals.external++
			case strings.HasPrefix(file, "vendor/") || strings.Contains(file, "/vendor/"):
				totals.vendor++
			case strings.HasPrefix(file, "support/thirdparty/"):
				totals.copied++
			case *fn.Generated:
				totals.generated++
			default:
				findings = append(findings, finding{pkg.Path, fn.Name, file, fn.Position.Line, fn.Position.Col})
			}
		}
	}
	slices.SortFunc(findings, func(a, b finding) int {
		for _, pair := range [][2]string{{a.Package, b.Package}, {a.Function, b.Function}, {a.File, b.File}} {
			if c := cmp.Compare(pair[0], pair[1]); c != 0 {
				return c
			}
		}
		if c := cmp.Compare(a.Line, b.Line); c != 0 {
			return c
		}
		return cmp.Compare(a.Column, b.Column)
	})
	count := len(findings)
	findings = slices.Compact(findings)
	totals.duplicates = count - len(findings)
	return findings, totals, nil
}

// sourcePath returns an empty path for sources outside the root module.
func sourcePath(root, file string) (string, error) {
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	} else if resolved, err := filepath.EvalSymlinks(file); err == nil {
		file = resolved
	}
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return "", fmt.Errorf("normalizing source %s: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil
	}
	file, err = filepath.EvalSymlinks(file)
	if err != nil {
		return "", fmt.Errorf("resolving source %s: %w", rel, err)
	}
	rel, err = filepath.Rel(root, file)
	if err != nil {
		return "", fmt.Errorf("normalizing resolved source %s: %w", file, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", fmt.Errorf("checking source %s: %w", rel, err)
	}
	if !info.Mode().IsRegular() || filepath.Ext(file) != ".go" {
		return "", fmt.Errorf("source %s is not a regular Go file", rel)
	}
	// Nested modules are not first-party declarations in the analyzed module.
	for dir := filepath.Dir(file); dir != root; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return "", nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("checking module boundary for %s: %w", rel, err)
		}
	}
	return filepath.ToSlash(rel), nil
}

func writeReports(artifacts string, findings []finding, totals scanTotals, metadata scanMetadata) (resultErr error) {
	stage, err := os.MkdirTemp(artifacts, ".deadcode-")
	if err != nil {
		return fmt.Errorf("staging deadcode reports: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("removing staged reports: %w", err))
		}
	}()
	data, err := json.MarshalIndent(findings, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding findings: %w", err)
	}
	var text strings.Builder
	packages := 0
	previous := ""
	for _, fn := range findings {
		fmt.Fprintf(&text, "%s:%d:%d: %s.%s\n", fn.File, fn.Line, fn.Column, fn.Package, fn.Function)
		if fn.Package != previous {
			packages++
			previous = fn.Package
		}
	}
	summary := fmt.Sprintf(`Deadcode analysis completed (reporting only; candidates require review).
Commit: %s
Tracked source dirty: %s
Tool: golang.org/x/tools/cmd/deadcode %s
Go: %s
Environment: %s
Memory settings: GOMEMLIMIT=%s GOGC=%s (not a process/container memory limit)
Scope: root module ./...; executable roots and -test
Tags: %s
Exclusions: generated declarations, vendor (including HyperShift API), support/thirdparty, sources outside the root module
Excluded tag: envtest; generator.go references cfg, k8sClient and ctx defined only in suite_test.go, so the non-test package variant cannot load
Analysis elapsed: %s
Analyzer candidates before source filtering: %d
Generated exclusions: %d
Vendor exclusions: %d
Copied-code exclusions: %d
Outside-root-module exclusions: %d
Duplicate records removed: %d
Reported candidates: %d
Packages with candidates: %d
`, metadata.commit, metadata.dirty, metadata.toolVersion, metadata.goVersion, scanEnvironment, metadata.memoryLimit, metadata.gc, scanTags, metadata.elapsed,
		totals.candidates, totals.generated, totals.vendor, totals.copied, totals.external, totals.duplicates, len(findings), packages)
	contents := [][]byte{append(data, '\n'), []byte(text.String()), []byte(summary)}
	for i, name := range reportNames {
		if err := os.WriteFile(filepath.Join(stage, name), contents[i], 0644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	// Publish the successful summary last, after both candidate reports exist.
	for _, name := range reportNames {
		if err := os.Rename(filepath.Join(stage, name), filepath.Join(artifacts, name)); err != nil {
			return fmt.Errorf("publishing %s: %w", name, err)
		}
	}
	return nil
}
