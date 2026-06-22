package fake

import (
	"github.com/openshift/hypershift/support/capabilities"
)

var _ capabilities.CapabiltyChecker = &FakeSupportAllCapabilities{}
var _ capabilities.CapabiltyChecker = &FakeSupportNoCapabilities{}
var _ capabilities.CapabiltyChecker = &FakeCapabilitiesSupportAllExcept{}

type FakeSupportAllCapabilities struct{}

func (f *FakeSupportAllCapabilities) Has(capabilities ...capabilities.CapabilityType) bool {
	return true
}

type FakeSupportNoCapabilities struct{}

func (f *FakeSupportNoCapabilities) Has(capabilities ...capabilities.CapabilityType) bool {
	return false
}

func NewSupportAllExcept(capas ...capabilities.CapabilityType) capabilities.CapabiltyChecker {
	notSupported := make(map[capabilities.CapabilityType]struct{}, len(capas))
	for _, capability := range capas {
		notSupported[capability] = struct{}{}
	}
	return &FakeCapabilitiesSupportAllExcept{NotSupported: notSupported}
}

type FakeCapabilitiesSupportAllExcept struct {
	NotSupported map[capabilities.CapabilityType]struct{}
}

func (f *FakeCapabilitiesSupportAllExcept) Has(capabilities ...capabilities.CapabilityType) bool {
	for _, capability := range capabilities {
		if _, notSupported := f.NotSupported[capability]; notSupported {
			return false
		}
	}

	return true
}
