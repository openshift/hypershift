package scenarioonly

import "testing"

func TestReconcileHappy(t *testing.T) { // want `test function "TestReconcileHappy" must be named "TestReconcile" for Reconcile; keep scenarios in table-driven cases or t.Run subtests`
	Reconcile()
}

func TestReconcileError(t *testing.T) { // want `test function "TestReconcileError" must be named "TestReconcile" for Reconcile; keep scenarios in table-driven cases or t.Run subtests`
	Reconcile()
}
