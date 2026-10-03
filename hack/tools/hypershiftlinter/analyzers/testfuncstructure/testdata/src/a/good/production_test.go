package good

import (
	"fmt"
	"testing"
)

func TestReconcile(t *testing.T) {
	t.Run("When input is valid, it should reconcile", func(t *testing.T) {
		Reconcile()
	})
	t.Run("When input is invalid, it should return an error", func(t *testing.T) {
		Reconcile()
	})
}

func TestBuildThing(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "When the input is valid, it should build the thing"},
		{name: "When the input is empty, it should build the default thing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buildThing()
		})
	}
}

func TestReconcileLonger(t *testing.T) {
	ReconcileLonger()
}

func TestController_Sync(t *testing.T) {
	controller := &Controller{}
	controller.Sync()
}

func TestController_Reconcile(t *testing.T) {
	controller := &Controller{}
	controller.reconcile()
}

// A bare method test is accepted when its call identifies one receiver.
func TestSync(t *testing.T) {
	controller := &OtherController{}
	controller.Sync()
}

// A bare method test is accepted without call evidence when only one method has
// that name.
func TestStart(t *testing.T) {}

func TestWorker_Run(t *testing.T) {
	instance := &worker{}
	instance.Run()
}

// Package-level behavior with no production function call has no meaningful
// one-function mapping.
func TestConstants(t *testing.T) {
	if fmt.Sprint(1) != "1" {
		t.Fatal("unexpected formatting")
	}
}

func testHelper() {}

func TestTestHelper(t *testing.T) {
	testHelper()
}

func TestGeneratedUtilityScenario(t *testing.T) {
	GeneratedUtility()
}

//nolint:hypershiftlinter // testfuncstructure: intentional compatibility scenario
func TestReconcileCompatibility(t *testing.T) {
	Reconcile()
}

func TestMain(m *testing.M) {}

func BenchmarkReconcile(b *testing.B) {}

func FuzzReconcile(f *testing.F) {}

func ExampleReconcile() {}

type suite struct{}

func (*suite) TestReconcile(t *testing.T) {}

func Test_Reconcile(t *testing.T) {}

func Test(t *testing.T) { // want `test function "Test" must be named "TestReconcile" to map to Reconcile; use one top-level test with table-driven cases or t.Run subtests, or add a documented //nolint:hypershiftlinter exception`
	Reconcile()
}
