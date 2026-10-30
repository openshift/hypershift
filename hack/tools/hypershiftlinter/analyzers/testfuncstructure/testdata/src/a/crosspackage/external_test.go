package crosspackage_test

import (
	"testing"

	"a/crosspackage"
)

func TestValidate(t *testing.T) { // want `test function "TestValidate" duplicates "TestValidate" for Validate; consolidate scenarios under "TestValidate" using table-driven cases or t.Run subtests`
	crosspackage.Validate()
}

func TestWorker_Run(t *testing.T) { // want `test function "TestWorker_Run" adds another top-level test for Worker.Run while internal test "TestRun" still exists; consolidate both under "TestWorker_Run" using table-driven cases or t.Run subtests`
	worker := &crosspackage.Worker{}
	worker.Run()
}

func TestOther_Run(t *testing.T) {
	other := &crosspackage.Other{}
	other.Run()
}
