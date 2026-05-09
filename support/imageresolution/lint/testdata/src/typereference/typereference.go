package typereference

import "example.com/releaseinfo"

var _ releaseinfo.ProviderWithRegistryOverrides = nil // want "reference to deprecated type"

type Controller struct {
	provider releaseinfo.ProviderWithOpenShiftImageRegistryOverrides // want "reference to deprecated type"
}
