package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/crdify/pkg/config"
	"sigs.k8s.io/crdify/pkg/runner"
	"sigs.k8s.io/crdify/pkg/validations"
	kyaml "sigs.k8s.io/yaml"
)

// crdDirs is a custom flag type that collects multiple --crd-dir values.
type crdDirs []string

func (d *crdDirs) String() string { return strings.Join(*d, ",") }
func (d *crdDirs) Set(val string) error {
	*d = append(*d, val)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var dirs crdDirs
	comparisonBase := flag.String("comparison-base", "main",
		"git ref to compare CRD schemas against (branch, tag, or SHA)")
	configFile := flag.String("config", "",
		"path to a crdify configuration file (optional)")
	flag.Var(&dirs, "crd-dir", "directory containing CRD YAML files to check (can be specified multiple times)")
	flag.Parse()

	if len(dirs) == 0 {
		return fmt.Errorf("at least one --crd-dir must be specified")
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return fmt.Errorf("finding repository root: %w", err)
	}

	baseHash, err := resolveBaseHash(repoRoot, *comparisonBase)
	if err != nil {
		return fmt.Errorf("resolving comparison base %q: %w", *comparisonBase, err)
	}

	cfg, err := config.Load(*configFile)
	if err != nil {
		return fmt.Errorf("loading crdify config: %w", err)
	}

	crdifyRunner, err := runner.New(cfg, runner.DefaultRegistry())
	if err != nil {
		return fmt.Errorf("configuring crdify runner: %w", err)
	}

	var allErrors []string
	var allWarnings []string
	totalChecked := 0

	for _, dir := range dirs {
		absDir := dir
		if !filepath.IsAbs(dir) {
			absDir = filepath.Join(repoRoot, dir)
		}

		results, err := checkCRDsInDir(repoRoot, absDir, baseHash, crdifyRunner)
		if err != nil {
			return fmt.Errorf("checking CRDs in %s: %w", dir, err)
		}

		totalChecked += results.checked
		allErrors = append(allErrors, results.errors...)
		allWarnings = append(allWarnings, results.warnings...)
	}

	for _, w := range allWarnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
	}

	if len(allErrors) > 0 {
		fmt.Fprintf(os.Stderr, "\nFAIL: CRD schema check failed with %d error(s) across %d CRD(s):\n\n", len(allErrors), totalChecked)
		for _, e := range allErrors {
			fmt.Fprintf(os.Stderr, "  ERROR: %s\n", e)
		}
		fmt.Fprintf(os.Stderr, "\nIf this is an intentional API change, see the crdify documentation for exception mechanisms.\n")
		fmt.Fprintf(os.Stderr, "For pre-existing violations from moved/renamed files, use Prow: /override verify-crd-schema\n")
		return fmt.Errorf("breaking CRD schema changes detected")
	}

	fmt.Fprintf(os.Stderr, "PASS: CRD schema check passed (%d CRDs checked, %d warnings)\n", totalChecked, len(allWarnings))
	return nil
}

// checkResults holds the results from checking a set of CRDs.
type checkResults struct {
	checked  int
	errors   []string
	warnings []string
}

// checkCRDsInDir walks a directory recursively and checks all CRD YAML files
// for breaking changes against the base commit. Non-CRD YAML files encountered
// during the walk (e.g., envtest TestSuite fixtures) are silently skipped via
// isCRDYAML content-based filtering. Featuregate CRD variants (-Default and
// -TechPreviewNoUpgrade) are also skipped; only the CustomNoUpgrade superset
// variant is checked to avoid false positives when fields move between gates.
func checkCRDsInDir(repoRoot, dir string, baseHash string, crdifyRunner *runner.Runner) (checkResults, error) {
	var results checkResults

	entries, err := os.ReadDir(dir)
	if err != nil {
		return results, fmt.Errorf("reading directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			subResults, err := checkCRDsInDir(repoRoot, path, baseHash, crdifyRunner)
			if err != nil {
				return results, err
			}
			results.checked += subResults.checked
			results.errors = append(results.errors, subResults.errors...)
			results.warnings = append(results.warnings, subResults.warnings...)
			continue
		}

		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return results, fmt.Errorf("reading file %s: %w", path, err)
		}

		if !isCRDYAML(data) {
			continue
		}

		newCRD, err := readCRD(data)
		if err != nil {
			return results, fmt.Errorf("parsing CRD from %s: %w", path, err)
		}

		newCRD = filterVersionsWithSchema(newCRD)
		if len(newCRD.Spec.Versions) == 0 {
			continue
		}

		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return results, fmt.Errorf("computing relative path for %s: %w", path, err)
		}

		oldCRD, err := loadCRDFromCommit(repoRoot, baseHash, relPath)
		if err != nil {
			return results, fmt.Errorf("loading CRD from base commit for %s: %w", relPath, err)
		}
		if oldCRD == nil {
			fmt.Fprintf(os.Stderr, "info: %s is new (not found in base), skipping comparison\n", relPath)
			continue
		}
		if len(oldCRD.Spec.Versions) == 0 {
			fmt.Fprintf(os.Stderr, "info: %s exists in base but has no versions with schemas, skipping comparison\n", relPath)
			continue
		}

		compResults := compareCRDs(oldCRD, newCRD, crdifyRunner)
		results.checked++

		for _, cr := range compResults.CRDValidation {
			for _, e := range cr.Errors {
				results.errors = append(results.errors, fmt.Sprintf("%s: %s: %s", relPath, cr.Name, e))
			}
			for _, w := range cr.Warnings {
				results.warnings = append(results.warnings, fmt.Sprintf("%s: %s: %s", relPath, cr.Name, w))
			}
		}

		for _, vr := range compResults.SameVersionValidation {
			collectPropertyResults(&results, relPath, fmt.Sprintf("version %s", vr.Version), vr.PropertyComparisons)
		}

		for _, vr := range compResults.ServedVersionValidation {
			collectPropertyResults(&results, relPath, fmt.Sprintf("served version %s", vr.Version), vr.PropertyComparisons)
		}
	}

	return results, nil
}

// collectPropertyResults extracts errors and warnings from property comparison
// results into the check results, using the provided version label for formatting.
func collectPropertyResults(results *checkResults, relPath, versionLabel string, comparisons []validations.PropertyComparisonResult) {
	for _, pr := range comparisons {
		for _, cr := range pr.ComparisonResults {
			for _, e := range cr.Errors {
				results.errors = append(results.errors, fmt.Sprintf("%s: %s: %s: %s: %s", relPath, versionLabel, pr.Property, cr.Name, e))
			}
			for _, w := range cr.Warnings {
				results.warnings = append(results.warnings, fmt.Sprintf("%s: %s: %s: %s: %s", relPath, versionLabel, pr.Property, cr.Name, w))
			}
		}
	}
}

// resolveBaseHash uses Git's revision and repository handling, including linked
// worktrees and branch configuration that go-git's parser does not accept.
func resolveBaseHash(repoRoot, ref string) (string, error) {
	data, err := gitOutput(repoRoot, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// loadCRDFromCommit retains the path-based comparison: a new or renamed CRD
// has no base at that path and is not compared. Git is used only to read blobs;
// schema parsing and compatibility validation remain in Go.
func loadCRDFromCommit(repoRoot, hash, path string) (*apiextensionsv1.CustomResourceDefinition, error) {
	if hash == "" {
		return nil, nil
	}
	path = filepath.ToSlash(path)
	entry, err := gitOutput(repoRoot, "ls-tree", "-z", "--full-tree", hash, "--", ":(literal)"+path)
	if err != nil {
		return nil, err
	}
	if len(entry) == 0 {
		return nil, nil
	}
	data, err := gitOutput(repoRoot, "show", hash+":"+path)
	if err != nil {
		return nil, err
	}
	crd, err := readCRD(data)
	if err != nil {
		return nil, err
	}
	return filterVersionsWithSchema(crd), nil
}
func gitOutput(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return out, nil
}

// readCRD unmarshals YAML data into a CustomResourceDefinition.
func readCRD(data []byte) (*apiextensionsv1.CustomResourceDefinition, error) {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := kyaml.Unmarshal(data, crd); err != nil {
		return nil, err
	}
	return crd, nil
}

// isCRDYAML checks whether the YAML data represents a CustomResourceDefinition.
func isCRDYAML(data []byte) bool {
	obj := &metav1.PartialObjectMetadata{}
	if err := kyaml.Unmarshal(data, obj); err != nil {
		return false
	}
	return obj.APIVersion == apiextensionsv1.SchemeGroupVersion.String() && obj.Kind == "CustomResourceDefinition"
}

// filterVersionsWithSchema returns a copy of the CRD with only versions that have OpenAPI schemas.
func filterVersionsWithSchema(crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
	out := crd.DeepCopy()
	filtered := make([]apiextensionsv1.CustomResourceDefinitionVersion, 0, len(out.Spec.Versions))
	for _, v := range out.Spec.Versions {
		if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
			filtered = append(filtered, v)
		}
	}
	out.Spec.Versions = filtered
	return out
}

// findRepoRoot walks up from cwd to find the .git directory.
func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no .git directory found above %s", cwd)
}

// compareCRDs is the testable core of the comparison logic.
// Returns an empty result when oldCRD is nil (new CRD, nothing to compare against).
func compareCRDs(oldCRD, newCRD *apiextensionsv1.CustomResourceDefinition, crdifyRunner *runner.Runner) *runner.Results {
	if oldCRD == nil {
		return &runner.Results{}
	}
	return crdifyRunner.Run(oldCRD, newCRD)
}
