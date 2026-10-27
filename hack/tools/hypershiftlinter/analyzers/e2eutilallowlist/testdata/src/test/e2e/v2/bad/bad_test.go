package bad

import (
	. "github.com/onsi/ginkgo/v2"
	"testing"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	v "github.com/openshift/hypershift/test/e2e/util"
)

func TestDisallowedSymbols(t *testing.T) {
	_ = e2eutil.IsLessThan("4.14", "4.23") // want `reference to github.com/openshift/hypershift/test/e2e/util.IsLessThan is not allowed from test/e2e/v2`

	_ = e2eutil.AtLeast("4.23", "4.14") // want `reference to github.com/openshift/hypershift/test/e2e/util.AtLeast is not allowed from test/e2e/v2`

	_ = e2eutil.IsGreaterThanOrEqualTo("4.23", "4.14") // want `reference to github.com/openshift/hypershift/test/e2e/util.IsGreaterThanOrEqualTo is not allowed from test/e2e/v2`

	_ = e2eutil.SetReleaseImageVersion() // want `reference to github.com/openshift/hypershift/test/e2e/util.SetReleaseImageVersion is not allowed from test/e2e/v2`

	_ = e2eutil.ShouldRunKarpenterTests() // want `reference to github.com/openshift/hypershift/test/e2e/util.ShouldRunKarpenterTests is not allowed from test/e2e/v2`

	// Aliased import: proves the analyzer resolves through aliases
	_ = v.IsLessThan("4.14", "4.23") // want `reference to github.com/openshift/hypershift/test/e2e/util.IsLessThan is not allowed from test/e2e/v2`
}

// Previously globally allowed symbols must also be rejected in new callers.
func TestNewCaller(t *testing.T) {
	_, _ = e2eutil.GetConfig()              // want `reference to .*util.GetConfig is not allowed`
	_ = e2eutil.Version414                  // want `reference to .*util.Version414 is not allowed`
	_ = e2eutil.VersionFuture               // want `reference to .*util.VersionFuture is not allowed`
	_ = e2eutil.Predicate[string](nil)      // want `reference to .*util.Predicate is not allowed`
	_ = v.GetConfig                         // want `reference to .*util.GetConfig is not allowed`
	func() { _, _ = e2eutil.GetConfig() }() // want `reference to .*util.GetConfig is not allowed`
}

func TestExistingCaller(t *testing.T) {
	_, _ = e2eutil.GetConfig()
	_ = e2eutil.Version414 // want `reference to .*util.Version414 is not allowed`
}

type Allowed struct{}
type Other struct{}

func (*Allowed) Method() { _, _ = e2eutil.GetConfig() }
func (*Other) Method()   { _, _ = e2eutil.GetConfig() } // want `reference to .*util.GetConfig is not allowed`
func Method()            { _, _ = e2eutil.GetConfig() } // want `reference to .*util.GetConfig is not allowed`

var packageReference = e2eutil.GetConfig     // want `reference to .*util.GetConfig is not allowed`
type PackageType = e2eutil.Predicate[string] // want `reference to .*util.Predicate is not allowed`

var _ = Describe("new registration", func() {
	_, _ = e2eutil.GetConfig() // want `reference to .*util.GetConfig is not allowed`
})
