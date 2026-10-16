package testingalias

import "testing"

type T = testing.T

func TestReconcile(t *T) {
	Reconcile()
}

func TestReconcileErrors(t *T) { // want `test function "TestReconcileErrors" also tests Reconcile; consolidate it into "TestReconcile" using table-driven cases or t.Run subtests`
	Reconcile()
}
