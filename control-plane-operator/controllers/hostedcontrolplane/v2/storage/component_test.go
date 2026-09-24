package storage

import (
	"testing"
)

// TestNewComponentConstruction verifies that NewComponent constructs the
// cluster-storage-operator component successfully. The component always gains a
// precondition on ConfigOperatorReconciliationSucceeded so the HCCO creates the
// ClusterCSIDriver before the CSO starts; the precondition gating behavior itself
// is covered by the framework tests in support/controlplane-component.
func TestNewComponentConstruction(t *testing.T) {
	comp := NewComponent()
	if comp == nil {
		t.Fatal("NewComponent returned nil")
	}
	if got := comp.Name(); got != ComponentName {
		t.Errorf("component name = %q, want %q", got, ComponentName)
	}
}
