package ambiguous

import "testing"

func TestReconcile(t *testing.T) { // want `test function "TestReconcile" does not map unambiguously to one production function \(candidates: First.Reconcile, Second.Reconcile\); use the canonical Test<FunctionName> or Test<Receiver>_<Method> form, or add a documented //nolint:hypershiftlinter exception`
}

func TestLifecycle(t *testing.T) { // want `test function "TestLifecycle" does not map unambiguously to one production function \(candidates: First.Reconcile, Second.Reconcile\); use the canonical Test<FunctionName> or Test<Receiver>_<Method> form, or add a documented //nolint:hypershiftlinter exception`
	first := &First{}
	first.Reconcile()
	second := &Second{}
	second.Reconcile()
}

func TestFirst_Reconcile(t *testing.T) {
	first := &First{}
	first.Reconcile()
}

func TestSecondReconcile(t *testing.T) {
	second := &Second{}
	second.Reconcile()
}
