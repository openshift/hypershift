// Package main implements a CRD schema checker that detects breaking changes
// in CustomResourceDefinition schemas by comparing the current working tree
// against a git base reference. It leverages the openshift/crd-schema-checker
// library to enforce API compatibility rules such as no field removals,
// no enum removals, no new required fields, and no data type changes.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/openshift/crd-schema-checker/pkg/cmd/options"
	"github.com/openshift/crd-schema-checker/pkg/resourceread"
	"github.com/spf13/pflag"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	kyaml "sigs.k8s.io/yaml"
)

// comparatorsDisabledByKAL lists comparators that are already enforced by the
// kube-api-linter plugin in golangci-lint and should not be double-checked here.
var comparatorsDisabledByKAL = []string{
	"NoBools",
	"NoFloats",
	"NoUints",
	"NoMaps",
	"ConditionsMustHaveProperSSATags",
}

func main() {
	var crdDirs string
	var comparisonBase string

	pflag.StringVar(&crdDirs, "crd-dirs", "", "Comma-separated list of directories containing CRD YAML files to validate")
	pflag.StringVar(&comparisonBase, "comparison-base", "", "Git ref (branch, tag, or SHA) to compare against")
	pflag.Parse()

	if crdDirs == "" {
		fmt.Fprintln(os.Stderr, "error: --crd-dirs is required")
		pflag.Usage()
		os.Exit(1)
	}
	if comparisonBase == "" {
		fmt.Fprintln(os.Stderr, "error: --comparison-base is required")
		pflag.Usage()
		os.Exit(1)
	}

	rawDirs := strings.Split(crdDirs, ",")
	dirs := make([]string, 0, len(rawDirs))
	for _, dir := range rawDirs {
		if trimmed := strings.TrimSpace(dir); trimmed != "" {
			dirs = append(dirs, trimmed)
		}
	}
	exitCode, err := run(dirs, comparisonBase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}

// run executes the CRD schema comparison and returns 0 if no breaking changes
// are found, 1 if breaking changes are detected, or an error if the process fails.
func run(crdDirs []string, comparisonBase string) (int, error) {
	comparatorConfig, err := buildComparatorConfig()
	if err != nil {
		return 0, fmt.Errorf("failed to build comparator config: %w", err)
	}

	// Open the git repository from the current working directory.
	repo, err := git.PlainOpenWithOptions(".", &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to open git repository: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return 0, fmt.Errorf("failed to get git worktree: %w", err)
	}
	repoRoot := worktree.Filesystem.Root()

	baseHash, err := repo.ResolveRevision(plumbing.Revision(comparisonBase))
	if err != nil {
		return 0, fmt.Errorf("failed to resolve git revision %q: %w", comparisonBase, err)
	}

	baseCommit, err := repo.CommitObject(*baseHash)
	if err != nil {
		return 0, fmt.Errorf("failed to get commit object for %q: %w", comparisonBase, err)
	}

	var totalErrors int
	var totalWarnings int

	for _, dir := range crdDirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return 0, fmt.Errorf("failed to resolve absolute path for %q: %w", dir, err)
		}

		errs, warnings, err := checkDirectory(absDir, repoRoot, baseCommit, comparisonBase, comparatorConfig)
		if err != nil {
			return 0, fmt.Errorf("failed to check directory %q: %w", dir, err)
		}
		totalErrors += errs
		totalWarnings += warnings
	}

	fmt.Fprintf(os.Stderr, "\nCRD schema check complete: %d error(s), %d warning(s)\n", totalErrors, totalWarnings)

	if totalErrors > 0 {
		return 1, nil
	}
	return 0, nil
}

// buildComparatorConfig creates the comparator configuration with the same
// defaults used by the openshift/api schema checker, disabling comparators
// that are already enforced by kube-api-linter.
func buildComparatorConfig() (*options.ComparatorConfig, error) {
	comparatorOptions := options.NewComparatorOptions()

	defaultSet := sets.New[string](comparatorOptions.DefaultEnabledComparators...)
	comparatorOptions.DefaultEnabledComparators = sets.List(defaultSet.Delete(comparatorsDisabledByKAL...))

	if err := comparatorOptions.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate comparator options: %w", err)
	}

	return comparatorOptions.Complete()
}

// checkDirectory walks a directory for CRD YAML files and compares each against
// its version in the git base commit.
func checkDirectory(dir, repoRoot string, baseCommit *object.Commit, baseName string, config *options.ComparatorConfig) (int, int, error) {
	var totalErrors int
	var totalWarnings int

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".yaml" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		if !isCRDFile(data) {
			return nil
		}

		newCRD, err := resourceread.ReadCustomResourceDefinitionV1(data)
		if err != nil {
			return fmt.Errorf("failed to parse CRD from %s: %w", path, err)
		}

		if !hasVersionedSchema(newCRD) {
			return nil
		}

		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return fmt.Errorf("failed to compute relative path for %s: %w", path, err)
		}

		oldCRD, err := loadCRDFromCommit(baseCommit, relPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load %s from %s: %v (file may be new)\n", relPath, baseName, err)
			return nil
		}
		if oldCRD == nil {
			fmt.Fprintf(os.Stderr, "warning: %s in %s has no schema; skipping comparison\n", relPath, baseName)
			return nil
		}

		errs, warnings := compareCRDs(relPath, oldCRD, newCRD, config)
		totalErrors += errs
		totalWarnings += warnings
		return nil
	})

	return totalErrors, totalWarnings, err
}

// compareCRDs runs the configured comparators on two CRD versions and prints results.
func compareCRDs(path string, oldCRD, newCRD *apiextensionsv1.CustomResourceDefinition, config *options.ComparatorConfig) (int, int) {
	var totalErrors int
	var totalWarnings int

	comparisonResults, errs := config.ComparatorRegistry.Compare(oldCRD, newCRD, config.ComparatorNames...)
	for _, err := range errs {
		fmt.Fprintf(os.Stderr, "ERROR %s: %v\n", path, err)
		totalErrors++
	}

	for _, result := range comparisonResults {
		for _, msg := range result.Errors {
			fmt.Fprintf(os.Stderr, "ERROR %s: %s: %s\n", path, result.Name, msg)
			totalErrors++
		}
		for _, msg := range result.Warnings {
			fmt.Fprintf(os.Stderr, "WARNING %s: %s: %s\n", path, result.Name, msg)
			totalWarnings++
		}
		for _, msg := range result.Infos {
			fmt.Fprintf(os.Stderr, "INFO %s: %s: %s\n", path, result.Name, msg)
		}
	}

	return totalErrors, totalWarnings
}

// isCRDFile checks if the given YAML data represents a CustomResourceDefinition.
func isCRDFile(data []byte) bool {
	partialObject := &metav1.PartialObjectMetadata{}
	if err := kyaml.Unmarshal(data, partialObject); err != nil {
		return false
	}
	return partialObject.APIVersion == apiextensionsv1.SchemeGroupVersion.String() &&
		partialObject.Kind == "CustomResourceDefinition"
}

// hasVersionedSchema returns true if at least one version in the CRD has an
// OpenAPI v3 schema defined.
func hasVersionedSchema(crd *apiextensionsv1.CustomResourceDefinition) bool {
	for _, version := range crd.Spec.Versions {
		if version.Schema != nil && version.Schema.OpenAPIV3Schema != nil {
			return true
		}
	}
	return false
}

// loadCRDFromCommit loads a CRD YAML file from a specific git commit.
// Returns nil if the file does not exist in the commit (i.e., a new file).
func loadCRDFromCommit(commit *object.Commit, relPath string) (*apiextensionsv1.CustomResourceDefinition, error) {
	file, err := commit.File(relPath)
	if err != nil {
		return nil, fmt.Errorf("file not found in commit: %w", err)
	}

	contents, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("failed to read file contents: %w", err)
	}

	crd, err := resourceread.ReadCustomResourceDefinitionV1([]byte(contents))
	if err != nil {
		return nil, fmt.Errorf("failed to parse CRD: %w", err)
	}

	if !hasVersionedSchema(crd) {
		return nil, nil
	}

	return crd, nil
}
