package good

import (
	"testing"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

func TestAllowedSymbols(t *testing.T) {
	_, _ = e2eutil.GetConfig()

	v414 := e2eutil.Version414
	v51 := e2eutil.Version51
	_ = v414
	_ = v51

	_ = e2eutil.Predicate[string](nil)
}
