package schemacheck

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/openshift/api/tools/codegen/pkg/generation"
	"github.com/openshift/crd-schema-checker/pkg/cmd/options"
	"github.com/openshift/crd-schema-checker/pkg/resourceread"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kyaml "sigs.k8s.io/yaml"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
)

// Options contains the configuration required for the schemacheck generator.
type Options struct {
	// Disabled indicates whether the schemacheck generator is disabled or not.
	// This defaults to false as the schemacheck generator is enabled by default.
	Disabled bool

	// EnabledComparators is a list of the comparators that should be enabled.
	// If this is empty, the default comparators are enabled.
	EnabledComparators []string

	// DisabledComparators is a list of the comparators that should be disabled.
	// If this is empty, no default comparators are disabled.
	DisabledComparators []string

	// ComparisonBase is the base branch/commit to compare against.
	// This defaults to "master".
	// This is not exposed via configuration as it must be set globally.
	ComparisonBase string
}

// generator implements the generation.Generator interface.
// It is designed to verify the CRD schema updates for a particular API group.
type generator struct {
	disabled            bool
	enabledComparators  []string
	disabledComparators []string
	comparisonBase      string
}

// NewGenerator builds a new schemacheck generator.
func NewGenerator(opts Options) generation.Generator {
	return &generator{
		disabled:            opts.Disabled,
		enabledComparators:  opts.EnabledComparators,
		disabledComparators: opts.DisabledComparators,
		comparisonBase:      opts.ComparisonBase,
	}
}

// ApplyConfig creates returns a new generator based on the configuration passed.
// If the schemacheck configuration is empty, the existing generation is returned.
func (g *generator) ApplyConfig(config *generation.Config) generation.Generator {
	if config == nil || config.SchemaCheck == nil {
		return g
	}

	return NewGenerator(Options{
		Disabled:            config.SchemaCheck.Disabled,
		EnabledComparators:  config.SchemaCheck.EnabledValidators,
		DisabledComparators: config.SchemaCheck.DisabledValidators,
		ComparisonBase:      g.comparisonBase,
	})
}

// Name returns the name of the generator.
func (g *generator) Name() string {
	return "schemacheck"
}

// GenGroup runs the schemacheck generator against the given group context.
func (g *generator) GenGroup(groupCtx generation.APIGroupContext) error {
	if g.disabled {
		klog.V(2).Infof("Skipping API schema check for %s", groupCtx.Name)
		return nil
	}

	errs := []error{}

	comparatorOptions := options.NewComparatorOptions()
	comparatorOptions.EnabledComparators = g.enabledComparators
	comparatorOptions.DisabledComparators = g.disabledComparators

	if err := comparatorOptions.Validate(); err != nil {
		return fmt.Errorf("could not validate comparator options: %w", err)
	}

	comparatorConfig, err := comparatorOptions.Complete()
	if err != nil {
		return fmt.Errorf("could not complete comparator options: %w", err)
	}

	for _, version := range groupCtx.Versions {
		klog.V(1).Infof("Verifying API schema for for %s/%s", groupCtx.Name, version.Name)

		if err := g.genGroupVersion(groupCtx.Name, version, comparatorConfig); err != nil {
			errs = append(errs, fmt.Errorf("could not run schemacheck generator for group/version %s/%s: %w", groupCtx.Name, version.Name, err))
		}
	}

	if len(errs) > 0 {
		return kerrors.NewAggregate(errs)
	}

	return nil
}

// genGroupVersion runs the schemacheck generator against a particular version of the API group.
func (g *generator) genGroupVersion(group string, version generation.APIVersionContext, comparatorConfig *options.ComparatorConfig) error {
	contexts, err := loadSchemaCheckGenerationContextsForVersion(version, g.comparisonBase)
	if err != nil {
		return fmt.Errorf("could not load schema check generation contexts for group/version %s/%s: %w", group, version.Name, err)
	}

	if len(contexts) == 0 {
		klog.V(1).Infof("No CRD manifests found for %s/%s", group, version.Name)
		return nil
	}

	var manifestErrs []error

	for _, context := range contexts {
		klog.V(1).Infof("Verifying schema for %s\n", context.manifestName)
		comparisonResults, errs := comparatorConfig.ComparatorRegistry.Compare(context.oldCRD, context.manifestCRD, comparatorConfig.ComparatorNames...)
		if len(errs) > 0 {
			return fmt.Errorf("could not compare manifests for %s: %w", context.manifestName, kerrors.NewAggregate(errs))
		}

		for _, comparisonResult := range comparisonResults {
			for _, msg := range comparisonResult.Errors {
				manifestErrs = append(manifestErrs, fmt.Errorf("error in %s: %s: %v", context.manifestName, comparisonResult.Name, msg))
			}
		}

		for _, comparisonResult := range comparisonResults {
			for _, msg := range comparisonResult.Warnings {
				klog.Warningf("warning in %s: %s: %v", context.manifestName, comparisonResult.Name, msg)
			}
		}
		for _, comparisonResult := range comparisonResults {
			for _, msg := range comparisonResult.Infos {
				klog.Infof("info in %s: %s: %v", context.manifestName, comparisonResult.Name, msg)
			}
		}
	}

	if len(manifestErrs) > 0 {
		return kerrors.NewAggregate(manifestErrs)
	}

	return nil
}

// schemaCheckGenerationContext contains the context required to verify the schema for a particular
// CRD manifest.
type schemaCheckGenerationContext struct {
	manifestName string
	manifestCRD  *apiextensionsv1.CustomResourceDefinition
	oldCRD       *apiextensionsv1.CustomResourceDefinition
}

// loadSchemaCheckGenerationContextsForVersion loads the generation contexts for all the manifests
// within a particular API group version.
// It finds all CRD manifests, loads the data and the original version of the manifest for comparison.
func loadSchemaCheckGenerationContextsForVersion(version generation.APIVersionContext, gitBaseSHA string) ([]schemaCheckGenerationContext, error) {
	repo, err := git.PlainOpenWithOptions(version.Path, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		return nil, fmt.Errorf("could not open git repository at %s: %w", version.Path, err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("could not load git worktree for repository at %s: %w", version.Path, err)
	}

	repoBaseDir := worktree.Filesystem.Root()

	baseHash, err := repo.ResolveRevision(plumbing.Revision(gitBaseSHA))
	if err != nil {
		return nil, fmt.Errorf("could not resolve git revision %s: %w", gitBaseSHA, err)
	}

	baseCommit, err := repo.CommitObject(*baseHash)
	if err != nil {
		return nil, fmt.Errorf("could not resolve git commit %s: %w", gitBaseSHA, err)
	}

	generationContexts, err := loadSchemaCheckGenerationContextsForVersionFromDir(version, baseCommit, repoBaseDir, version.Path, gitBaseSHA)
	if err != nil {
		return nil, fmt.Errorf("could not load schema check generation contexts from dir %q: %w", repoBaseDir, err)
	}

	return generationContexts, nil
}

func loadSchemaCheckGenerationContextsForVersionFromDir(version generation.APIVersionContext, baseCommit *object.Commit, repoBaseDir, searchPath, gitBaseSHA string) ([]schemaCheckGenerationContext, error) {
	var errs []error

	dirEntries, err := os.ReadDir(searchPath)
	if err != nil {
		return nil, fmt.Errorf("could not read file info for directory %s: %v", version.Path, err)
	}

	generationContexts := []schemaCheckGenerationContext{}

	for _, fileInfo := range dirEntries {
		if fileInfo.IsDir() {
			subContexts, err := loadSchemaCheckGenerationContextsForVersionFromDir(version, baseCommit, repoBaseDir, filepath.Join(searchPath, fileInfo.Name()), gitBaseSHA)
			if err != nil {
				errs = append(errs, fmt.Errorf("could not load schema check generation contexts from dir %q: %v", filepath.Join(searchPath, fileInfo.Name()), err))
				continue
			}

			generationContexts = append(generationContexts, subContexts...)
			continue
		}

		if filepath.Ext(fileInfo.Name()) != ".yaml" {
			continue
		}

		path := filepath.Join(searchPath, fileInfo.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("could not read file %s: %v", path, err))
			continue
		}

		partialObject := &metav1.PartialObjectMetadata{}
		if err := kyaml.Unmarshal(data, partialObject); err != nil {
			errs = append(errs, fmt.Errorf("could not unmarshal YAML for type meta inspection: %v", err))
			continue
		}

		// Ignore any file that doesn't have a kind of CustomResourceDefinition.
		if !isCustomResourceDefinition(partialObject) {
			continue
		}

		crd, err := resourceread.ReadCustomResourceDefinitionV1(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("could not read CustomResourceDefinition from file %s: %v", path, err))
			continue
		}

		hasVersionedSchema := false
		for i, version := range crd.Spec.Versions {
			if version.Schema != nil && version.Schema.OpenAPIV3Schema != nil {
				hasVersionedSchema = true
				break
			} else {
				// Remove the version if it doesn't have a schema in case there are multiple versions.
				crd.Spec.Versions = append(crd.Spec.Versions[:i], crd.Spec.Versions[i+1:]...)
			}
		}

		if !hasVersionedSchema {
			klog.V(1).Infof("Skipping schema check for %s as it does not have a versioned schema", path)
			continue
		}

		oldFilePath, err := filepath.Rel(repoBaseDir, path)
		if err != nil {
			errs = append(errs, fmt.Errorf("could not determine relative path for file %s: %v", path, err))
			continue
		}

		var oldCRD *apiextensionsv1.CustomResourceDefinition

		oldFile, err := baseCommit.File(oldFilePath)
		if err != nil {
			klog.Warningf("could not find file %s in git commit %s: %v, file may be new", oldFilePath, gitBaseSHA, err)
		}
		if oldFile != nil {
			oldData, err := oldFile.Contents()
			if err != nil {
				errs = append(errs, fmt.Errorf("could not read file %s from git commit %s: %v", oldFilePath, gitBaseSHA, err))
				continue
			}

			oldCRD, err = resourceread.ReadCustomResourceDefinitionV1([]byte(oldData))
			if err != nil {
				errs = append(errs, fmt.Errorf("could not read CustomResourceDefinition from file %s in git commit %s: %v", oldFilePath, gitBaseSHA, err))
				continue
			}

			hasVersionedSchema := false
			for i, version := range oldCRD.Spec.Versions {
				if version.Schema != nil && version.Schema.OpenAPIV3Schema != nil {
					hasVersionedSchema = true
					break
				} else {
					// Remove the version if it doesn't have a schema in case there are multiple versions.
					oldCRD.Spec.Versions = append(oldCRD.Spec.Versions[:i], oldCRD.Spec.Versions[i+1:]...)
				}
			}

			if !hasVersionedSchema {
				// We still want to check the new schema.
				oldCRD = nil
			}
		}

		generationContexts = append(generationContexts, schemaCheckGenerationContext{
			manifestName: oldFilePath,
			manifestCRD:  crd,
			oldCRD:       oldCRD,
		})
	}

	if len(errs) > 0 {
		return nil, kerrors.NewAggregate(errs)
	}

	return generationContexts, nil
}

// isCustomResourceDefinition returns true if the object is a CustomResourceDefinition.
// This is determined by the object having a Kind of CustomResourceDefinition and the
// correct APIVersion.
func isCustomResourceDefinition(partialObject *metav1.PartialObjectMetadata) bool {
	return partialObject.APIVersion == apiextensionsv1.SchemeGroupVersion.String() && partialObject.Kind == "CustomResourceDefinition"
}
