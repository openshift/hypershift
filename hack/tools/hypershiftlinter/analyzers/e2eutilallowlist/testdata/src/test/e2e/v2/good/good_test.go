package good

import "testing"

// TestNoV1Imports verifies that v2 code with no reference to test/e2e/util
// produces no diagnostics.
func TestNoV1Imports(t *testing.T) {
	t.Log("no v1 util imports — no diagnostics expected")
}
