//go:build envtest

package envtest

// SuiteSpec defines a test suite specification.
// This is compatible with the openshift/api tests YAML format.
type SuiteSpec struct {
	// Name is the name of the test suite.
	Name string `json:"name"`

	// CRDName is the full CRD name (e.g., hostedclusters.hypershift.openshift.io).
	CRDName string `json:"crdName"`

	// FeatureGates is the list of featureGates that must be enabled/disabled for this test to be run.
	FeatureGates []string `json:"featureGates"`

	// Version is the version of the CRD under test in this file.
	// When omitted, if there is a single version in the CRD, this is assumed to be the correct version.
	Version string `json:"version,omitempty"`

	// Tests defines the test cases to run for this test suite.
	Tests TestSpec `json:"tests"`

	// PerTestRuntimeInfo cannot be specified in the testcase itself, but at runtime must be computed.
	PerTestRuntimeInfo *PerTestRuntimeInfo `json:"-"`
}

// PerTestRuntimeInfo is computed at runtime and contains the CRD files to test against.
type PerTestRuntimeInfo struct {
	// CRDFilenames indicates the CRD filenames that match this test's CRDName (for patching).
	CRDFilenames []string `json:"-"`
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

	// InitialCRDPatches is a list of YAML patches to apply to the CRD before applying
	// the initial version of the resource.
	// Once the initial version has been applied, the CRD will be restored to its
	// original state before the updated object is applied.
	// This can be used to test ratcheting validation of CRD schema changes over time.
	InitialCRDPatches []Patch `json:"initialCRDPatches"`

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

// Patch represents a single operation to be applied to a YAML document.
// It follows the JSON Patch format as defined in RFC 6902.
type Patch struct {
	// Op is the operation to be performed. Common operations include "add", "remove", "replace", "move", "copy", and "test".
	Op string `json:"op"`

	// Path is a JSON Pointer that indicates the location in the YAML document where the operation is to be performed.
	Path string `json:"path"`

	// Value is the value to be used within the operation. This field is required for operations like "add" and "replace".
	Value *interface{} `json:"value"`
}
