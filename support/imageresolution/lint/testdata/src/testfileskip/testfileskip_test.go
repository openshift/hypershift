package testfileskip

// No diagnostics expected — test files are exempt from param and struct field checks.

func helperWithOverrides(registryOverrides map[string]string) {
	_ = registryOverrides
}

type testController struct {
	registryOverrides map[string]string
}

var _ = testController{}
