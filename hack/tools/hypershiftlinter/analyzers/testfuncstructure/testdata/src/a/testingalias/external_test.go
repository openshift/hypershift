package testingalias_test

import (
	"testing"

	"a/testingalias"
)

type testingT = testing.T
type T = testingT

func TestReconcile(t *T) { // want `test function "TestReconcile" duplicates "TestReconcile" for Reconcile; consolidate scenarios under "TestReconcile" using table-driven cases or t.Run subtests`
	testingalias.Reconcile()
}

func TestReconcileErrors(t *T) { // want `test function "TestReconcileErrors" also tests Reconcile; consolidate it into "TestReconcile" using table-driven cases or t.Run subtests`
	testingalias.Reconcile()
}
