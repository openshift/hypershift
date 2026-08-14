package generation

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"

	"golang.org/x/tools/go/packages"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
)

// skipDirs is a default list of directories to skip when looking for API group versions.
var skipDirs = sets.NewString(
	".git",
	"vendor",
)

// Context is the top level generation context passed into each generator.
type Context struct {
	// BaseDir is the top level directory in which to search for API packages
	// and in which to run the generators.
	BaseDir string

	// APIGroups contains a list of API Groups and information regarding
	// their generation.
	APIGroups []APIGroupContext
}

// APIGroupContext is the context gathered for a particular API group.
type APIGroupContext struct {
	// Name is the group name.
	Name string

	// Versions is a list of API versions found within the group.
	Versions []APIVersionContext

	// Config is the group's generation configuration.
	// This is populated from the `.codegen.yaml` configuration for the API group.
	Config *Config
}

// APIVersionContext is the context gathered for a particular API version.
type APIVersionContext struct {
	// Name is the version name.
	Name string

	// Path is the path to the folder containing the API version.
	Path string

	// PackagePath is the path to the package containing the API version.
	// This is the import path for the package.
	PackagePath string

	// PackageName is the golang packagh name for the API version.
	PackageName string
}

// Options represents the base configuration used to generate a context.
type Options struct {
	// BaseDir is the base directory in which to run the generators.
	BaseDir string

	// APIGroupVersions is a list of API group versions to generate.
	// When omitted, all discovered API group versions are generated.
	APIGroupVersions []string
}

// NewContext creates a generation context from the provided options.
func NewContext(opts Options) (Context, error) {
	baseDir, err := filepath.Abs(opts.BaseDir)
	if err != nil {
		return Context{}, fmt.Errorf("could not get absolute path for base dir: %w", err)
	}

	apiGroups, err := newAPIGroupsContext(baseDir, opts.APIGroupVersions)
	if err != nil {
		return Context{}, fmt.Errorf("could not build API Groups context: %w", err)
	}

	if err := getAPIGroupConfigs(apiGroups); err != nil {
		return Context{}, fmt.Errorf("could not get API Group configs: %w", err)
	}

	return Context{
		BaseDir:   baseDir,
		APIGroups: apiGroups,
	}, nil
}

// newAPIGroupsContext discovers API group information from the base directory given
// and filters that information to the required group versions provided.
// If no group versions are provided all group versions discoverd will be returned.
func newAPIGroupsContext(baseDir string, requiredGroupVersions []string) ([]APIGroupContext, error) {
	goPackages, err := loadPackagesFromDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("could not load Go packages: %w", err)
	}

	apiGroups, err := findAPIGroups(goPackages, requiredGroupVersions)
	if err != nil {
		return nil, fmt.Errorf("could not filter API Groups from Go packages: %w", err)
	}

	out := []APIGroupContext{}
	for group, versions := range apiGroups {
		out = append(out, APIGroupContext{
			Name:     group,
			Versions: versions,
		})
	}

	// Sort the API Groups alphabetically so that we have a stable ordering.
	sort.Slice(out, func(i int, j int) bool {
		return out[i].Name < out[j].Name
	})

	return out, nil
}

// loadPackagesFromDir walks through a list of directories and looks for those
// that look like Go packages.
func loadPackagesFromDir(baseDir string) (map[string]*packages.Package, error) {
	outPkgs := make(map[string]*packages.Package)

	if err := filepath.WalkDir(baseDir, func(path string, dirEntry fs.DirEntry, _ error) error {
		if !dirEntry.IsDir() {
			// We only care about directories.
			return nil
		}

		if skipDirs.Has(filepath.Base(path)) {
			// This directory and any subdirectories should be skipped.
			return filepath.SkipDir
		}

		cfg := &packages.Config{
			Dir:  path,
			Logf: klog.V(4).Infof,
		}

		pkgs, err := packages.Load(cfg)
		if err != nil {
			return fmt.Errorf("could not load package at path %s: %w", path, err)
		}

		if len(pkgs) != 1 {
			return fmt.Errorf("unexpected number of go packages found for path %s: %d", path, len(pkgs))
		}

		if len(pkgs[0].GoFiles) == 0 {
			// No go files means there's nothing parse, skip this directory but continue to subfolders.
			return nil
		}

		outPkgs[path] = pkgs[0]

		klog.V(3).Infof("Found directory: %s and package: %+v", path, pkgs[0])

		return nil
	}); err != nil {
		return nil, fmt.Errorf("could not walk directory %s: %w", baseDir, err)
	}

	return outPkgs, nil
}

// findAPIGroups looks through a list of go packages to identify those that
// contain a GroupVersion declaration.
// And then builds a map of groups to their versions and the folders that contain
// each version.
// This is used to identify the group and version information for a package.
func findAPIGroups(goPackages map[string]*packages.Package, desiredGroupVersions []string) (map[string][]APIVersionContext, error) {
	apiGroups := make(map[string][]APIVersionContext)
	desired := sets.NewString(desiredGroupVersions...)

	for pkgPath, pkg := range goPackages {
		fileSet := token.NewFileSet()

		pkgs, err := parser.ParseDir(fileSet, pkgPath, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("could not parse directory for package %s: %+v", pkgPath, pkgs)
		}

		for _, p := range pkgs {
			gvv := &groupVersionVisitor{}
			ast.Walk(gvv, p)

			// If a group was found and either the desired list is empty or contains this group version,
			// add it to the output.
			if gvv.groupVersion.String() != "" && (desired.Len() == 0 || desired.Has(gvv.groupVersion.String())) {
				klog.V(3).Infof("Found GroupVersion in path %s: %+v", pkgPath, gvv.groupVersion)
				group := gvv.groupVersion.Group
				version := gvv.groupVersion.Version

				apiGroups[group] = append(apiGroups[group], APIVersionContext{
					Name:        version,
					Path:        pkgPath,
					PackagePath: pkg.PkgPath,
					PackageName: p.Name,
				})
			} else {
				klog.V(3).Infof("No GroupVersion found in path %s", pkgPath)
			}
		}
	}

	return apiGroups, nil
}

type groupVersionVisitor struct {
	groupVersion schema.GroupVersion
}

// Visit runs through each declaration in the package to look for a GroupVersion declaration.
// When it finds this declaration, it expects to have two values, a Group value and Kind value.
// These are then extracted as the Group and Version for this package.
// Should only walk a single package at once.
func (g *groupVersionVisitor) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		return nil
	}

	genDecl, ok := node.(*ast.GenDecl)
	if !ok {
		// An assigment is a generic declaration, so ignore anything that isn't a generic declaration.
		// Return g so that we continue the walk.
		return g
	}

	groupVersionValue, err := getGroupVersionDecl(genDecl)
	if err != nil {
		klog.Errorf("Error with group version declaration: %v", err)
		return nil
	}

	// This declaration doesn't contain the GroupVersion.
	if groupVersionValue == nil {
		return g
	}

	g.groupVersion.Group, err = getValueOf(groupVersionValue, "Group")
	if err != nil {
		klog.Errorf("Error getting Group from declaration: %v", err)
		return nil
	}

	g.groupVersion.Version, err = getValueOf(groupVersionValue, "Version")
	if err != nil {
		klog.Errorf("Error getting Version from declaration: %v", err)
		return nil
	}

	// We found the GroupVersion declaration so we can stop the walk.
	return nil
}

// getGroupVersionDecl extracts the GroupVersion declaration from the generic
// declaration. GroupVersion is expected to be a schema.GroupVersion.
func getGroupVersionDecl(genDecl *ast.GenDecl) (*ast.CompositeLit, error) {
	for _, spec := range genDecl.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}

		if len(valueSpec.Names) < 1 {
			// A declaration with no name?
			continue
		}

		if valueSpec.Names[0].Name != "GroupVersion" {
			continue
		}

		if len(valueSpec.Values) < 1 {
			// A declaration with no value?
			// The group version declaration cannot be valid.
			return nil, fmt.Errorf("GroupVersion declaration does not have expected number of values, found %d values, expected 1 value", len(valueSpec.Values))
		}

		value, ok := valueSpec.Values[0].(*ast.CompositeLit)
		if !ok {
			// The GroupVersion cannot be a schema.GroupVersion so stop the walk.
			return nil, fmt.Errorf("expected GroupVersion declaration to be a composite literal, but got %T", valueSpec.Values[0])
		}

		return value, nil
	}

	return nil, nil
}

// getValueOf gets the value of a key within the composite literal as a string.
func getValueOf(value *ast.CompositeLit, name string) (string, error) {
	for _, elt := range value.Elts {
		expr, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return "", fmt.Errorf("expected a KeyValue expression, got %T", elt)
		}

		key, ok := expr.Key.(*ast.Ident)
		if !ok {
			return "", fmt.Errorf("expected Key to be an ident, got %T", expr.Key)
		}

		if key.Name != name {
			continue
		}

		switch t := expr.Value.(type) {
		case *ast.BasicLit:
			return strconv.Unquote(t.Value)
		case *ast.Ident:
			return strconv.Unquote(t.Obj.Decl.(*ast.ValueSpec).Values[0].(*ast.BasicLit).Value)
		default:
			return "", fmt.Errorf("unknown type for key %s: %T", name, expr.Value)
		}
	}

	return "", nil
}
