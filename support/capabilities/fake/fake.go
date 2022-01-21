package fake

import (
	"github.com/openshift/hypershift/support/capabilities"
)

var _ capabilities.CapabiltyChecker = &FakeSupportAllCapabilities{}

type FakeSupportAllCapabilities struct {
}

func (f *FakeSupportAllCapabilities) Has(capabilities ...capabilities.CapabilityType) bool {
	return true
}
