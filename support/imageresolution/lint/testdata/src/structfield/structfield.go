package structfield

type Controller struct {
	registryOverrides              map[string]string   // want "raw override map field"
	openShiftImageRegistryOverrides map[string][]string // want "raw override map field"
	normalField                    string
}

var _ = Controller{}
