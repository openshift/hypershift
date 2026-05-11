package rawmap

func ProcessOverrides(registryOverrides map[string]string) { // want "raw override map parameter"
	_ = registryOverrides
}

func ProcessMirrors(imageRegistryOverrides map[string][]string) { // want "raw override map parameter"
	_ = imageRegistryOverrides
}

func Allowed(data map[string]string) {
	_ = data
}
