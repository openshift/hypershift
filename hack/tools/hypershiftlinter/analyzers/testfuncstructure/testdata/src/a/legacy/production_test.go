package legacy

import "testing"

func TestReconcile(t *testing.T) { // want `test function "TestReconcile" adds another top-level test for Reconcile while legacy test "TestReconcileErrors" still exists; consolidate both under "TestReconcile" using table-driven cases or t.Run subtests`
	Reconcile()
}

func TestReconcileErrors(t *testing.T) {
	Reconcile()
}
