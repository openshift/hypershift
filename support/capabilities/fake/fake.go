package fake

import (
	"github.com/openshift/hypershift/support/capabilities"
)

var (
	_ capabilities.CapabiltyChecker = &FakeSupportAllCapabilities{}
	_ capabilities.CapabiltyChecker = &FakeSupportNoCapabilities{}
	_ capabilities.CapabiltyChecker = &FakeCapabilitiesSupportAllExcept{}
)

type FakeSupportAllCapabilities struct{}

func (*FakeSupportAllCapabilities) Has(capabilities ...capabilities.CapabilityType) bool {
	return true
}

type FakeSupportNoCapabilities struct{}

func (*FakeSupportNoCapabilities) Has(capabilities ...capabilities.CapabilityType) bool {
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
