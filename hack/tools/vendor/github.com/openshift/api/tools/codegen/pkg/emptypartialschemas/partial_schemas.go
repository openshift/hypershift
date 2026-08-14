package emptypartialschemas

import (
	"fmt"
	"github.com/openshift/api/tools/codegen/pkg/utils"
	"io"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"path/filepath"
	"sigs.k8s.io/yaml"
	"strings"

	"k8s.io/gengo/args"
	gengogenerator "k8s.io/gengo/generator"
	"k8s.io/gengo/namer"
	"k8s.io/gengo/types"

	"k8s.io/klog/v2"
)

// CustomArgs is used tby the go2idl framework to pass args specific to this
// generator.
type CustomArgs struct {
	BoundingDirs []string // Only deal with types rooted under these dirs.
}

// Remove it and use PublicNamer instead.
func featureGatedPartialSchemasNamer() *namer.NameStrategy {
	return &namer.NameStrategy{
		Join: func(pre string, in []string, post string) string {
			return strings.Join(in, "_")
		},
		PrependPackageNames: 1,
	}
}

// NameSystems returns the name system used by the generators in this package.
func NameSystems() namer.NameSystems {
	return namer.NameSystems{
		"public": featureGatedPartialSchemasNamer(),
		"raw":    namer.NewRawNamer("", nil),
	}
}

// DefaultNameSystem returns the default name system for ordering the types to be
// processed by the generators in this package.
func DefaultNameSystem() string {
	return "public"
}

type generatorResultGatherer struct {
	crdNamesToFeatureGates map[string]*CRDInfo
}

type CRDInfo struct {
	CRDName                  string
	TopLevelFeatureGates     []string
	GroupName                string
	Version                  string
	PluralName               string
	KindName                 string
	Scope                    string
	HasStatus                bool
	FeatureGates             []string
	ShortNames               []string
	Category                 string
	ApprovedPRNumber         string
	FilenameRunLevel         string
	FilenameOperatorName     string
	FilenameOperatorOrdering string
	Capability               string
	PrinterColumns           []apiextensionsv1.CustomResourceColumnDefinition
	Annotations              map[string]string
	Labels                   map[string]string
}

func (g *generatorResultGatherer) Packages(context *gengogenerator.Context, arguments *args.GeneratorArgs) gengogenerator.Packages {
	inputs := sets.NewString(context.Inputs...)
	packages := gengogenerator.Packages{}

	boundingDirs := []string{}
	if customArgs, ok := arguments.CustomArgs.(*CustomArgs); ok {
		if customArgs.BoundingDirs == nil {
			customArgs.BoundingDirs = context.Inputs
		}
		for i := range customArgs.BoundingDirs {
			// Strip any trailing slashes - they are not exactly "correct" but
			// this is friendlier.
			boundingDirs = append(boundingDirs, strings.TrimRight(customArgs.BoundingDirs[i], "/"))
		}
	}

	for i := range inputs {
		klog.V(5).Infof("Considering pkg %q", i)
		pkg := context.Universe[i]
		if pkg == nil {
			// If the input had no Go files, for example.
			continue
		}

		ptagValue, exists, _ := extractStringTagFromComments(pkg.Comments, openshiftPackageGenerationEnablementMarkerName)
		if exists {
			if ptagValue != "true" {
				klog.Fatalf("Package %v: unsupported %s value: %q", i, openshiftPackageGenerationEnablementMarkerName, ptagValue)
			}
		} else {
			klog.V(5).Infof("  no tag")
		}

		// If the pkg-scoped tag says to generate, we can skip scanning types.
		pkgNeedsGeneration := (ptagValue == "true")
		if !pkgNeedsGeneration {
			// If the pkg-scoped tag did not exist, scan all types for one that
			// explicitly wants generation. Ensure all types that want generation
			// can be copied.
			for _, t := range pkg.Types {
				klog.V(5).Infof("  considering type %q", t.Name.String())
				typeTag := isCRDType(t)
				if typeTag == "true" {
					klog.V(5).Infof("    tag=true")
					pkgNeedsGeneration = true
				}
			}
		}

		if pkgNeedsGeneration {
			klog.Infof("Package %q needs generation", pkg.Name)
			groupName, ok, err := extractStringTagFromComments(pkg.Comments, tagGroupName)
			if err != nil {
				klog.Fatal(err)
			}
			if !ok {
				klog.Fatal("missing groupName")
			}
			path := pkg.Path
			// if the source path is within a /vendor/ directory (for example,
			// k8s.io/kubernetes/vendor/k8s.io/apimachinery/pkg/apis/meta/v1), allow
			// generation to output to the proper relative path (under vendor).
			// Otherwise, the generator will create the file in the wrong location
			// in the output directory.
			// TODO: build a more fundamental concept in gengo for dealing with modifications
			// to vendored packages.
			if strings.HasPrefix(pkg.SourcePath, arguments.OutputBase) {
				expandedPath := strings.TrimPrefix(pkg.SourcePath, arguments.OutputBase)
				if strings.Contains(expandedPath, "/vendor/") {
					path = expandedPath
				}
			}
			packages = append(packages,
				&gengogenerator.DefaultPackage{
					PackageName: strings.Split(filepath.Base(pkg.Path), ".")[0],
					PackagePath: path,
					HeaderText:  nil,
					GeneratorFunc: func(c *gengogenerator.Context) (generators []gengogenerator.Generator) {
						return []gengogenerator.Generator{
							NewGenFeatureGatedPartialSchemas(arguments.OutputFileBaseName, pkg.Path, g, groupName),
						}
					},
					FilterFunc: func(c *gengogenerator.Context, t *types.Type) bool {
						return t.Name.Package == pkg.Path
					},
				})
		}
	}

	return packages
}

// genFeatureGatedPartialSchemas produces a file with autogenerated deep-copy functions.
type genFeatureGatedPartialSchemas struct {
	gengogenerator.DefaultGen
	targetPackage  string
	imports        namer.ImportTracker
	typesForInit   []*types.Type
	groupName      string
	crdInfoTracker *generatorResultGatherer
}

func NewGenFeatureGatedPartialSchemas(sanitizedName, targetPackage string, crdInfoTracker *generatorResultGatherer, groupName string) gengogenerator.Generator {
	return &genFeatureGatedPartialSchemas{
		DefaultGen: gengogenerator.DefaultGen{
			OptionalName: sanitizedName,
		},
		targetPackage:  targetPackage,
		imports:        gengogenerator.NewImportTracker(),
		typesForInit:   make([]*types.Type, 0),
		crdInfoTracker: crdInfoTracker,
		groupName:      groupName,
	}
}

func (g *genFeatureGatedPartialSchemas) Filename() string {
	return g.DefaultGen.OptionalName
}

func (g *genFeatureGatedPartialSchemas) FileType() string {
	return "yaml"
}

func (g *genFeatureGatedPartialSchemas) Init(c *gengogenerator.Context, w io.Writer) error {
	c.FileTypes["yaml"] = utils.NewGengoJSONFile()
	return nil
}

func (g *genFeatureGatedPartialSchemas) Namers(c *gengogenerator.Context) namer.NameSystems {
	// Have the raw namer for this file track what it imports.
	return namer.NameSystems{
		"raw": namer.NewRawNamer(g.targetPackage, g.imports),
	}
}

func (g *genFeatureGatedPartialSchemas) Filter(c *gengogenerator.Context, t *types.Type) bool {
	return true
}

func underlyingType(t *types.Type) *types.Type {
	for t.Kind == types.Alias {
		t = t.Underlying
	}
	return t
}

func (g *genFeatureGatedPartialSchemas) Imports(c *gengogenerator.Context) (imports []string) {
	return []string{}
}

func (g *genFeatureGatedPartialSchemas) needsGeneration(t *types.Type) bool {
	isCRDTypeValue := isCRDType(t)
	if len(isCRDTypeValue) == 0 {
		return false
	}
	if isCRDTypeValue != "true" && isCRDTypeValue != "false" {
		klog.Fatalf("Type %v: unsupported %s value: %q", t, openshiftPackageGenerationEnablementMarkerName, isCRDTypeValue)
	}
	if isCRDTypeValue == "false" {
		// The whole package is being generated, but this type has opted out.
		klog.Infof("Not generating for type %v because type opted out", t)
		return false
	}
	if isCRDTypeValue != "true" {
		// The whole package is NOT being generated, and this type has NOT opted in.
		klog.Infof("Not generating for type %v because type did not opt in", t)
		return false
	}
	return true
}

func (g *genFeatureGatedPartialSchemas) GenerateType(c *gengogenerator.Context, t *types.Type, w io.Writer) error {
	if !g.needsGeneration(t) {
		klog.V(5).Infof("  Type %q is skipped: %q", t, isCRDType(t))
		return nil
	}
	klog.V(1).Infof("  Type %q needs partial schema files", t)

	// these featuregates control whether the type is generated at all.  The others control whether the type
	// has certain validation
	topLevelTypeConditional := extractStringSliceTagForType(t, openshiftFeatureGateMarkerName)

	topLevelFeatureGates := extractFeatureGatesForType(t)
	allOtherFeatureGates := g.generateFor(t)

	allFeatureGates := sets.NewString()
	allFeatureGates.Insert(topLevelFeatureGates...)
	allFeatureGates.Insert(allOtherFeatureGates...)

	// TODO this is wrong, lookup the crd name
	allResources := kubeBuilderCompatibleExtractValuesForMarkerForType(t, kubeBuilderResource)
	if len(allResources) == 0 {
		return fmt.Errorf("%v must specify %q", t.Name, kubeBuilderResource)
	}
	if len(allResources) > 1 {
		return fmt.Errorf("%v must only specify %q once", t.Name, kubeBuilderResource)
	}
	kubeBuilderResourceValues := allResources[0]

	filenameValues, err := extractNamedValuesForType(t, openshiftCRDFilenameMarkerName)
	if err != nil {
		return fmt.Errorf("failed extracting %q: %w", openshiftCRDFilenameMarkerName, err)
	}

	resourceName := kubeBuilderResourceValues["path"]
	scope := kubeBuilderResourceValues["scope"]
	if len(resourceName) == 0 {
		return fmt.Errorf("%v is missing path part of %q", t.Name, kubeBuilderResource)
	}
	if len(scope) == 0 {
		return fmt.Errorf("%v is missing scope part of %q", t.Name, kubeBuilderResource)
	}
	crdName := fmt.Sprintf("%s.%s", resourceName, g.groupName)
	annotations, labels := extractMetadataForType(t)
	crdInfo := &CRDInfo{
		CRDName:                  crdName,
		TopLevelFeatureGates:     topLevelTypeConditional,
		GroupName:                g.groupName,
		Version:                  filepath.Base(g.targetPackage),
		PluralName:               resourceName,
		KindName:                 t.Name.Name,
		Scope:                    scope,
		HasStatus:                tagExistsForType(t, kubeBuilderStatus),
		FeatureGates:             allFeatureGates.List(),
		ShortNames:               nil,
		Category:                 kubeBuilderResourceValues["categories"],
		ApprovedPRNumber:         extractStringTagForType(t, openshiftApprovedPRMarkerName),
		FilenameRunLevel:         filenameValues["cvoRunLevel"],
		FilenameOperatorName:     filenameValues["operatorName"],
		FilenameOperatorOrdering: filenameValues["operatorOrdering"],
		Capability:               extractStringTagForType(t, openshiftCapabilityMarkerName),
		PrinterColumns:           extractPrinterColumnsForType(t),
		Annotations:              annotations,
		Labels:                   labels,
	}
	if len(kubeBuilderResourceValues["shortName"]) > 0 {
		crdInfo.ShortNames = strings.Split(kubeBuilderResourceValues["shortName"], ";")
	}
	g.crdInfoTracker.crdNamesToFeatureGates[crdName] = crdInfo

	toSerialize := map[string]*CRDInfo{
		crdName: crdInfo,
	}
	content, err := yaml.Marshal(toSerialize)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%v\n", string(content))
	return err
}

// we use the system of shadowing 'in' and 'out' so that the same code is valid
// at any nesting level. This makes the autogenerator easy to understand, and
// the compiler shouldn't care.
func (g *genFeatureGatedPartialSchemas) generateFor(t *types.Type) []string {
	// derive inner types if t is an alias. We call the do* methods below with the alias type.
	// basic rule: generate according to inner type, but construct objects with the alias type.
	ut := underlyingType(t)

	ret := []string{}
	ret = append(ret, extractFeatureGatesFromComments(t.CommentLines)...)
	ret = append(ret, extractFeatureGatesFromComments(ut.CommentLines)...)

	var f func(*types.Type) []string
	switch ut.Kind {
	case types.Builtin:
		f = g.doBuiltin
	case types.Map:
		f = g.doMap
	case types.Slice:
		f = g.doSlice
	case types.Struct:
		f = g.doStruct
	case types.Pointer:
		f = g.doPointer
	case types.Interface:
		// interfaces are handled in-line in the other cases
		if t.String() == "k8s.io/apimachinery/pkg/runtime.Object" {
			return ret
		}
		klog.Fatalf("Hit an interface type %v. This should never happen.", t)
	case types.Alias:
		// can never happen because we branch on the underlying type which is never an alias
		klog.Fatalf("Hit an alias type %v. This should never happen.", t)
	default:
		klog.Fatalf("Hit an unsupported type %v.", t)
	}

	return append(ret, f(t)...)
}

// doBuiltin generates code for a builtin or an alias to a builtin. The generated code is
// is the same for both cases, i.e. it's the code for the underlying type.
func (g *genFeatureGatedPartialSchemas) doBuiltin(t *types.Type) []string {
	return nil
}

// doMap generates code for a map or an alias to a map. The generated code is
// is the same for both cases, i.e. it's the code for the underlying type.
func (g *genFeatureGatedPartialSchemas) doMap(t *types.Type) []string {
	ut := underlyingType(t)
	ret := []string{}
	ret = append(ret, g.generateFor(ut.Key)...)
	ret = append(ret, g.generateFor(ut.Elem)...)
	return ret
}

// doSlice generates code for a slice or an alias to a slice. The generated code is
// is the same for both cases, i.e. it's the code for the underlying type.
func (g *genFeatureGatedPartialSchemas) doSlice(t *types.Type) []string {
	ut := underlyingType(t)
	return g.generateFor(ut.Elem)
}

// doStruct generates code for a struct or an alias to a struct. The generated code is
// is the same for both cases, i.e. it's the code for the underlying type.
func (g *genFeatureGatedPartialSchemas) doStruct(t *types.Type) []string {
	ut := underlyingType(t)
	ret := []string{}

	// Now fix-up fields as needed.
	for _, m := range ut.Members {
		ret = append(ret, extractFeatureGatesFromComments(m.CommentLines)...)

		ret = append(ret, g.generateFor(m.Type)...)
	}
	return ret
}

// doPointer generates code for a pointer or an alias to a pointer. The generated code is
// is the same for both cases, i.e. it's the code for the underlying type.
func (g *genFeatureGatedPartialSchemas) doPointer(t *types.Type) []string {
	ut := underlyingType(t)
	uet := underlyingType(ut.Elem)
	return g.generateFor(uet)
}
