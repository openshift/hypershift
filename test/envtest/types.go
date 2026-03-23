package envtest

// SuiteSpec defines a test suite specification.
// This is compatible with the openshift/api tests YAML format.
type SuiteSpec struct {
	// Name is the name of the test suite.
	Name string `json:"name"`

	// CRDName is the full CRD name (e.g., hostedclusters.hypershift.openshift.io).
	CRDName string `json:"crdName"`

	// Version is the version of the CRD under test in this file.
	// When omitted, if there is a single version in the CRD, this is assumed to be the correct version.
	Version string `json:"version,omitempty"`

	// Tests defines the test cases to run for this test suite.
	Tests TestSpec `json:"tests"`
}

// TestSpec defines the test specs for individual tests in this suite.
type TestSpec struct {
	// OnCreate defines a list of on create style tests.
	OnCreate []OnCreateTestSpec `json:"onCreate"`

	// OnUpdate defines a list of on update style tests.
	OnUpdate []OnUpdateTestSpec `json:"onUpdate"`
}

// OnCreateTestSpec defines an individual test case for the on create style tests.
type OnCreateTestSpec struct {
	// Name is the name of this test case.
	Name string `json:"name"`

	// Initial is a literal string containing the initial YAML content from which to
	// create the resource.
	// Note `apiVersion` and `kind` fields are required though `metadata` can be omitted.
	Initial string `json:"initial"`

	// ExpectedError defines the error string that should be returned when the initial resource is invalid.
	// This will be matched as a substring of the actual error when non-empty.
	ExpectedError string `json:"expectedError"`

	// Expected is a literal string containing the expected YAML content that should be
	// persisted when the resource is created.
	// Note `apiVersion` and `kind` fields are required though `metadata` can be omitted.
	Expected string `json:"expected"`
}

// OnUpdateTestSpec defines an individual test case for the on update style tests.
type OnUpdateTestSpec struct {
	// Name is the name of this test case.
	Name string `json:"name"`

	// Initial is a literal string containing the initial YAML content from which to
	// create the resource.
	Initial string `json:"initial"`

	// Updated is a literal string containing the updated YAML content from which to
	// update the resource.
	Updated string `json:"updated"`

	// ExpectedError defines the error string that should be returned when the updated resource is invalid.
	ExpectedError string `json:"expectedError"`

	// ExpectedStatusError defines the error string that should be returned when the updated resource status is invalid.
	ExpectedStatusError string `json:"expectedStatusError"`

	// Expected is a literal string containing the expected YAML content that should be
	// persisted when the resource is updated.
	Expected string `json:"expected"`
}