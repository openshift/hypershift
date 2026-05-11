package lint

import (
	"go/ast"
	"go/types"
	"testing"

	. "github.com/onsi/gomega"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer_RawMap(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "rawmap")
}

func TestAnalyzer_ExemptImageresolutionPackage(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "example.com/imageresolution")
}

func TestAnalyzer_StructFields(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "structfield")
}

func TestAnalyzer_TypeReferences(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "typereference")
}

func TestAnalyzer_BannedImports(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "bannedimport")
}

func TestAnalyzer_TestFileSkip(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "testfileskip")
}

func TestIsBannedImport(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "When importing go-containerregistry, it should be banned",
			path:     "github.com/google/go-containerregistry/pkg/v1/remote",
			expected: true,
		},
		{
			name:     "When importing containers/image, it should be banned",
			path:     "github.com/containers/image/v5/transports",
			expected: true,
		},
		{
			name:     "When importing releaseinfo, it should not be banned",
			path:     "github.com/openshift/hypershift/support/releaseinfo",
			expected: false,
		},
		{
			name:     "When importing standard library, it should not be banned",
			path:     "fmt",
			expected: false,
		},
		{
			name:     "When importing imageresolution, it should not be banned",
			path:     "github.com/openshift/hypershift/support/imageresolution",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isBannedImport(tt.path)).To(Equal(tt.expected))
		})
	}
}

func TestIsBannedTypeName(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		expected bool
	}{
		{
			name:     "When type is ProviderWithRegistryOverrides, it should be banned",
			typeName: "ProviderWithRegistryOverrides",
			expected: true,
		},
		{
			name:     "When type is ProviderWithOpenShiftImageRegistryOverrides, it should be banned",
			typeName: "ProviderWithOpenShiftImageRegistryOverrides",
			expected: true,
		},
		{
			name:     "When type is RegistryMirrorProviderDecorator, it should be banned",
			typeName: "RegistryMirrorProviderDecorator",
			expected: true,
		},
		{
			name:     "When type is CommonRegistryProvider, it should be banned",
			typeName: "CommonRegistryProvider",
			expected: true,
		},
		{
			name:     "When type is Provider, it should not be banned",
			typeName: "Provider",
			expected: false,
		},
		{
			name:     "When type is ReleaseImage, it should not be banned",
			typeName: "ReleaseImage",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isBannedTypeName(tt.typeName)).To(Equal(tt.expected))
		})
	}
}

func TestIsOverrideParamName(t *testing.T) {
	tests := []struct {
		name     string
		param    string
		expected bool
	}{
		{
			name:     "When param is registryOverrides, it should match",
			param:    "registryOverrides",
			expected: true,
		},
		{
			name:     "When param is imageRegistryOverrides, it should match",
			param:    "imageRegistryOverrides",
			expected: true,
		},
		{
			name:     "When param is imageRegistryMirrors, it should match",
			param:    "imageRegistryMirrors",
			expected: true,
		},
		{
			name:     "When param is openShiftImageRegistryOverrides, it should match",
			param:    "openShiftImageRegistryOverrides",
			expected: true,
		},
		{
			name:     "When param is generic data, it should not match",
			param:    "data",
			expected: false,
		},
		{
			name:     "When param is annotations, it should not match",
			param:    "annotations",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isOverrideParamName(tt.param)).To(Equal(tt.expected))
		})
	}
}

func TestIsRawOverrideMap(t *testing.T) {
	t.Run("When type info is nil for the field, it should return false", func(t *testing.T) {
		g := NewWithT(t)

		// Construct a field with a matching override-pattern name and a map type
		// expression, but do NOT register the expression in TypesInfo.Types.
		// This causes TypesInfo.TypeOf to return nil, hitting the t == nil guard.
		field := &ast.Field{
			Names: []*ast.Ident{{Name: "registryOverrides"}},
			Type: &ast.MapType{
				Key:   ast.NewIdent("string"),
				Value: ast.NewIdent("string"),
			},
		}
		pass := &analysis.Pass{
			TypesInfo: &types.Info{},
		}

		g.Expect(isRawOverrideMap(pass, field)).To(BeFalse(),
			"should return false when type info cannot be resolved")
	})
}

func TestIsExempt(t *testing.T) {
	tests := []struct {
		name     string
		pkgPath  string
		expected bool
	}{
		{
			name:     "When package is imageresolution, it should be exempt",
			pkgPath:  "github.com/openshift/hypershift/support/imageresolution",
			expected: true,
		},
		{
			name:     "When package is imageresolution/lint, it should be exempt",
			pkgPath:  "github.com/openshift/hypershift/support/imageresolution/lint",
			expected: true,
		},
		{
			name:     "When package is support/util, it should be exempt",
			pkgPath:  "github.com/openshift/hypershift/support/util",
			expected: true,
		},
		{
			name:     "When package is ignition-server/cmd, it should be exempt",
			pkgPath:  "github.com/openshift/hypershift/ignition-server/cmd",
			expected: true,
		},
		{
			name:     "When package is hostedclusterconfigoperator, it should be exempt",
			pkgPath:  "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator",
			expected: true,
		},
		{
			name:     "When package is a controller, it should not be exempt",
			pkgPath:  "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/foo",
			expected: false,
		},
		{
			name:     "When package is catalogs, it should not be exempt",
			pkgPath:  "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/catalogs",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isExempt(tt.pkgPath)).To(Equal(tt.expected))
		})
	}
}
