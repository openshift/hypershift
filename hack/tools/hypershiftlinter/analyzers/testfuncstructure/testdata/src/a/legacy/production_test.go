package legacy

import "testing"

func TestReconcile(t *testing.T) { // want `test function "TestReconcile" adds another top-level test for Reconcile while legacy test "TestReconcileErrors" still exists; consolidate both under "TestReconcile" using table-driven cases or t.Run subtests`
	Reconcile()
}

func TestReconcileErrors(t *testing.T) {
	Reconcile()
}

func TestWorker_Run(t *testing.T) {
	worker := &Worker{}
	worker.Run()
}

func TestWorkerRun(t *testing.T) { // want `test function "TestWorkerRun" duplicates "TestWorker_Run" for Worker.Run; consolidate scenarios under "TestWorker_Run" using table-driven cases or t.Run subtests`
	worker := &Worker{}
	worker.Run()
}
