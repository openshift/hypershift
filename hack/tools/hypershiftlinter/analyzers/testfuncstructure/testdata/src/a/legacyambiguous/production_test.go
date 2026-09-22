package legacyambiguous

import "testing"

func TestWorkflow(t *testing.T) {
	Alpha()
	Beta()
}

func TestAlpha(t *testing.T) { // want `test function "TestAlpha" adds another top-level test for Alpha while legacy test "TestWorkflow" still exists; consolidate both under "TestAlpha" using table-driven cases or t.Run subtests`
	Alpha()
}
