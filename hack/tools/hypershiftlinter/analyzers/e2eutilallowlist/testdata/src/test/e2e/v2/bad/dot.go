package bad

import . "github.com/openshift/hypershift/test/e2e/util"

func dotImport() {
	_, _ = GetConfig() // want `reference to .*util.GetConfig is not allowed`
}
