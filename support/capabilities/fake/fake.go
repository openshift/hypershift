package fake

import (
	"github.com/openshift/hypershift/support/capabilities"
)

var _ capabilities.CapabiltyChecker = &FakeSupportAllCapabilities{}
var _ capabilities.CapabiltyChecker = &FakeSupportNoCapabilities{}

type FakeSupportAllCapabilities struct{}

func (f *FakeSupportAllCapabilities) Has(capabilities ...capabilities.CapabilityType) bool {
	return true
}

type FakeSupportNoCapabilities struct{}

func (f *FakeSupportNoCapabilities) Has(capabilities ...capabilities.CapabilityType) bool {
	return false
}
