package bad

import (
	aliased "github.com/openshift/hypershift/test/e2e/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

func Bad() {
	e2eutil.AcceptsTB(nil)    // want `reference to github.com/openshift/hypershift/test/e2e/util.AcceptsTB is forbidden from test/e2e/v2: its function signature accepts testing.TB; port it to a v2 error-returning helper`
	_ = e2eutil.AcceptsAlias  // want `reference to github.com/openshift/hypershift/test/e2e/util.AcceptsAlias is forbidden from test/e2e/v2: its function signature accepts testing.TB; port it to a v2 error-returning helper`
	_ = aliased.AcceptsT      // want `reference to github.com/openshift/hypershift/test/e2e/util.AcceptsT is forbidden from test/e2e/v2: its function signature accepts testing.TB; port it to a v2 error-returning helper`
	_ = e2eutil.FunctionValue // want `reference to github.com/openshift/hypershift/test/e2e/util.FunctionValue is forbidden from test/e2e/v2: its function signature accepts testing.TB; port it to a v2 error-returning helper`
}
