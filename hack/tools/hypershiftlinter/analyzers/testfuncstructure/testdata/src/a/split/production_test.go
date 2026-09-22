package split

import "testing"

func TestReconcile(t *testing.T) {
	Reconcile()
}

func TestReconcileAggregatesErrors(t *testing.T) { // want `test function "TestReconcileAggregatesErrors" also tests Reconcile; consolidate it into "TestReconcile" using table-driven cases or t.Run subtests`
	Reconcile()
}

// The longer exact production name wins over the Reconcile prefix.
func TestReconcileErrors(t *testing.T) {
	ReconcileErrors()
}

func TestController_SyncFailure(t *testing.T) { // want `test function "TestController_SyncFailure" must be named "TestController_Sync" for Controller.Sync; keep scenarios in table-driven cases or t.Run subtests`
	controller := &Controller{}
	controller.Sync()
}

func TestDisconnectedName(t *testing.T) { // want `test function "TestDisconnectedName" must be named "TestReconcile" to map to Reconcile; use one top-level test with table-driven cases or t.Run subtests, or add a documented //nolint:hypershiftlinter exception`
	Reconcile()
}
