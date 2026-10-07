package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"k8s.io/apimachinery/pkg/util/sets"
)

func main() {
	if err := verifyAPIDependencies(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ API dependencies verification passed")
}

func verifyAPIDependencies() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return fmt.Errorf("failed to find repository root: %w", err)
	}

	apiModPath := filepath.Join(repoRoot, "api")
	allowedAPIModules, err := loadAllowedImports(apiModPath)
	if err != nil {
		return fmt.Errorf("failed to load allowed imports: %w", err)
	}

	rootMod, err := loadGoMod(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return err
	}

	apiMod, err := loadGoMod(filepath.Join(apiModPath, "go.mod"))
	if err != nil {
		return err
	}

	if err := verifyAllowedAPIDependencies(apiMod.file, allowedAPIModules); err != nil {
		return err
	}

	return verifySharedDependencies(rootMod, apiMod)
}

type goModFile struct {
	file      *modfile.File
	directory string
}

func loadGoMod(path string) (*goModFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	parsed, err := modfile.Parse(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	return &goModFile{
		file:      parsed,
		directory: filepath.Dir(path),
	}, nil
}

func verifyAllowedAPIDependencies(apiMod *modfile.File, allowedAPIModules sets.Set[string]) error {
	var violations []string
	for _, req := range apiMod.Require {
		if req.Indirect {
			continue
		}

		modulePath := req.Mod.Path
		if !allowedAPIModules.Has(modulePath) {
			violations = append(violations, modulePath)
		}
	}

	if len(violations) > 0 {
		return fmt.Errorf(`❌ Unauthorized API dependencies detected:

%s

The HyperShift API module has strict dependency restrictions to maintain:
- API stability and compatibility
- Minimal dependency footprint
- Clear separation between API and implementation

Before adding any new dependencies to the API module, you must:

1. Consult with API reviewers to discuss alternatives
2. Ensure the dependency is absolutely necessary for the API layer
3. Verify it doesn't introduce breaking changes or version conflicts
4. Update the allowlist in api/.imports_allowed after approval

If this dependency is approved by API reviewers, add it to the allowlist in:
api/.imports_allowed

For questions, reach out to the HyperShift API review team.`,
			formatViolations(violations))
	}

	return nil
}

type dependencyVersionMismatch struct {
	modulePath  string
	rootVersion string
	apiVersion  string
}

type replacementKey struct {
	modulePath string
	version    string
}

type replacementTarget struct {
	modulePath   string
	version      string
	literalPath  string
	resolvedPath string
}

type dependencyReplacementMismatch struct {
	key        replacementKey
	rootTarget *replacementTarget
	apiTarget  *replacementTarget
}

func verifySharedDependencies(rootMod, apiMod *goModFile) error {
	rootVersions := map[string]string{}
	for _, requirement := range rootMod.file.Require {
		rootVersions[requirement.Mod.Path] = requirement.Mod.Version
	}

	apiVersions := map[string]string{}
	var mismatches []dependencyVersionMismatch
	for _, requirement := range apiMod.file.Require {
		apiVersions[requirement.Mod.Path] = requirement.Mod.Version
		rootVersion, shared := rootVersions[requirement.Mod.Path]
		if !shared || rootVersion == requirement.Mod.Version {
			continue
		}

		mismatches = append(mismatches, dependencyVersionMismatch{
			modulePath:  requirement.Mod.Path,
			rootVersion: rootVersion,
			apiVersion:  requirement.Mod.Version,
		})
	}

	replacementMismatches := findReplacementMismatches(rootMod, apiMod, rootVersions, apiVersions)
	if len(mismatches) == 0 && len(replacementMismatches) == 0 {
		return nil
	}

	sort.Slice(mismatches, func(i, j int) bool {
		return mismatches[i].modulePath < mismatches[j].modulePath
	})

	var sections []string
	if len(mismatches) > 0 {
		var section strings.Builder
		section.WriteString("shared dependency versions do not match:\n")
		for _, mismatch := range mismatches {
			fmt.Fprintf(&section, "  %s\n", mismatch.modulePath)
			fmt.Fprintf(&section, "    go.mod:     %s\n", mismatch.rootVersion)
			fmt.Fprintf(&section, "    api/go.mod: %s\n", mismatch.apiVersion)
		}
		sections = append(sections, strings.TrimSuffix(section.String(), "\n"))
	}

	if len(replacementMismatches) > 0 {
		var section strings.Builder
		section.WriteString("shared dependency replacements do not match:\n")
		for _, mismatch := range replacementMismatches {
			fmt.Fprintf(&section, "  %s %s\n", mismatch.key.modulePath, formatReplacementVersion(mismatch.key.version))
			fmt.Fprintf(&section, "    go.mod:     %s\n", formatReplacementTarget(mismatch.rootTarget))
			fmt.Fprintf(&section, "    api/go.mod: %s\n", formatReplacementTarget(mismatch.apiTarget))
		}
		sections = append(sections, strings.TrimSuffix(section.String(), "\n"))
	}

	return errors.New(strings.Join(sections, "\n\n") + "\n\nalign shared requirements and replacements and run `make update`")
}

func findReplacementMismatches(rootMod, apiMod *goModFile, rootVersions, apiVersions map[string]string) []dependencyReplacementMismatch {
	sharedPaths := sets.New[string]()
	for modulePath := range rootVersions {
		if _, shared := apiVersions[modulePath]; shared {
			sharedPaths.Insert(modulePath)
		}
	}

	rootReplacements := replacementsByKey(rootMod, sharedPaths)
	apiReplacements := replacementsByKey(apiMod, sharedPaths)
	keys := sets.New[replacementKey]()
	for key := range rootReplacements {
		keys.Insert(key)
	}
	for key := range apiReplacements {
		keys.Insert(key)
	}

	var mismatches []dependencyReplacementMismatch
	for key := range keys {
		rootTarget, rootFound := rootReplacements[key]
		apiTarget, apiFound := apiReplacements[key]
		if rootFound && apiFound && replacementTargetsEqual(rootTarget, apiTarget) {
			continue
		}

		mismatch := dependencyReplacementMismatch{key: key}
		if rootFound {
			mismatch.rootTarget = &rootTarget
		}
		if apiFound {
			mismatch.apiTarget = &apiTarget
		}
		mismatches = append(mismatches, mismatch)
	}

	sort.Slice(mismatches, func(i, j int) bool {
		if mismatches[i].key.modulePath != mismatches[j].key.modulePath {
			return mismatches[i].key.modulePath < mismatches[j].key.modulePath
		}
		if mismatches[i].key.version != mismatches[j].key.version {
			return mismatches[i].key.version < mismatches[j].key.version
		}
		rootI := formatReplacementTarget(mismatches[i].rootTarget)
		rootJ := formatReplacementTarget(mismatches[j].rootTarget)
		if rootI != rootJ {
			return rootI < rootJ
		}
		return formatReplacementTarget(mismatches[i].apiTarget) < formatReplacementTarget(mismatches[j].apiTarget)
	})

	return mismatches
}

func replacementTargetsEqual(rootTarget, apiTarget replacementTarget) bool {
	if rootTarget.version == "" || apiTarget.version == "" {
		return rootTarget.version == apiTarget.version && rootTarget.resolvedPath == apiTarget.resolvedPath
	}
	return rootTarget.modulePath == apiTarget.modulePath && rootTarget.version == apiTarget.version
}

func replacementsByKey(goMod *goModFile, sharedPaths sets.Set[string]) map[replacementKey]replacementTarget {
	replacements := map[replacementKey]replacementTarget{}
	for _, replacement := range goMod.file.Replace {
		if !sharedPaths.Has(replacement.Old.Path) {
			continue
		}

		key := replacementKey{modulePath: replacement.Old.Path, version: replacement.Old.Version}
		target := replacementTarget{modulePath: replacement.New.Path, version: replacement.New.Version}
		if replacement.New.Version == "" {
			target.literalPath = replacement.New.Path
			if filepath.IsAbs(replacement.New.Path) {
				target.resolvedPath = filepath.Clean(replacement.New.Path)
			} else {
				target.resolvedPath = filepath.Clean(filepath.Join(goMod.directory, replacement.New.Path))
			}
			target.modulePath = ""
		}
		replacements[key] = target
	}
	return replacements
}

func formatReplacementVersion(version string) string {
	if version == "" {
		return "<all versions>"
	}
	return version
}

func formatReplacementTarget(target *replacementTarget) string {
	if target == nil {
		return "<missing>"
	}
	if target.version == "" {
		return fmt.Sprintf("=> %s (resolved: %s)", target.literalPath, target.resolvedPath)
	}
	return fmt.Sprintf("=> %s %s", target.modulePath, target.version)
}

func formatViolations(violations []string) string {
	var formatted []string
	for _, v := range violations {
		formatted = append(formatted, fmt.Sprintf("  • %s", v))
	}
	return strings.Join(formatted, "\n")
}

func loadAllowedImports(apiModPath string) (sets.Set[string], error) {
	allowedImportsPath := filepath.Join(apiModPath, ".imports_allowed")

	file, err := os.Open(allowedImportsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", allowedImportsPath, err)
	}
	defer file.Close()

	allowedModules := sets.New[string]()
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		allowedModules.Insert(line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", allowedImportsPath, err)
	}

	return allowedModules, nil
}

func findRepoRoot() (string, error) {
	// Start from current working directory and walk up to find .git directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	dir := cwd
	for {
		// Check if .git directory exists
		if fileExists(filepath.Join(dir, ".git")) {
			return dir, nil
		}

		// Check if we've reached the root
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("could not find repository root (no .git directory found)")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
