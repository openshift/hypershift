package v1

import "github.com/openshift/hypershift/test/e2e/util"

var _ = util.IsLessThan("4.14", "4.23")
