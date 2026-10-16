package externallegacy_test

import (
	"testing"

	"a/externallegacy"
)

func TestValidate(t *testing.T) { // want `test function "TestValidate" adds another top-level test for Validate while internal test "TestValidateErrors" still exists`
	externallegacy.Validate()
}

func TestWorker_Run(t *testing.T) { // want `test function "TestWorker_Run" duplicates "TestWorker_Run" for Worker.Run; consolidate scenarios under "TestWorker_Run" using table-driven cases or t.Run subtests`
	worker := &externallegacy.Worker{}
	worker.Run()
}
