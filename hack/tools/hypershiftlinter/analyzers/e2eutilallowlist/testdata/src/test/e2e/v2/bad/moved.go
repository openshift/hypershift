package bad

import "github.com/openshift/hypershift/test/e2e/util"

// An identical caller name in another file does not inherit its allowance.
func TestExistingCallerMoved() {
	_, _ = util.GetConfig() // want `reference to .*util.GetConfig is not allowed`
}
