package bad

import (
	"testing"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	v "github.com/openshift/hypershift/test/e2e/util"
)

func TestDisallowedSymbols(t *testing.T) {
	_ = e2eutil.IsLessThan("4.14", "4.23") // want `reference to github.com/openshift/hypershift/test/e2e/util.IsLessThan is not allowed from test/e2e/v2; add it to the e2eutilallowlist allowlist or refactor to remove the dependency`

	_ = e2eutil.AtLeast("4.23", "4.14") // want `reference to github.com/openshift/hypershift/test/e2e/util.AtLeast is not allowed from test/e2e/v2; add it to the e2eutilallowlist allowlist or refactor to remove the dependency`

	_ = e2eutil.IsGreaterThanOrEqualTo("4.23", "4.14") // want `reference to github.com/openshift/hypershift/test/e2e/util.IsGreaterThanOrEqualTo is not allowed from test/e2e/v2; add it to the e2eutilallowlist allowlist or refactor to remove the dependency`

	_ = e2eutil.SetReleaseImageVersion() // want `reference to github.com/openshift/hypershift/test/e2e/util.SetReleaseImageVersion is not allowed from test/e2e/v2; add it to the e2eutilallowlist allowlist or refactor to remove the dependency`

	_ = e2eutil.ShouldRunKarpenterTests() // want `reference to github.com/openshift/hypershift/test/e2e/util.ShouldRunKarpenterTests is not allowed from test/e2e/v2; add it to the e2eutilallowlist allowlist or refactor to remove the dependency`

	// Aliased import: proves the analyzer resolves through aliases
	_ = v.IsLessThan("4.14", "4.23") // want `reference to github.com/openshift/hypershift/test/e2e/util.IsLessThan is not allowed from test/e2e/v2; add it to the e2eutilallowlist allowlist or refactor to remove the dependency`
}
