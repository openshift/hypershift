package good

import (
	. "github.com/onsi/ginkgo/v2"
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
	func() { _, _ = e2eutil.GetConfig() }()
}

type Allowed struct{}

func (*Allowed) Method() {
	_, _ = e2eutil.GetConfig()
}

var _ = Describe("existing registration", func() {
	func() { _, _ = e2eutil.GetConfig() }()
})
