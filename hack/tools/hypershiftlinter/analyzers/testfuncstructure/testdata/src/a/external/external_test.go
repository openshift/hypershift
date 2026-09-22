package external_test

import (
	"testing"

	"a/external"
)

func TestValidate(t *testing.T) {
	external.Validate()
}

func TestValidateErrors(t *testing.T) { // want `test function "TestValidateErrors" also tests Validate; consolidate it into "TestValidate" using table-driven cases or t.Run subtests`
	external.Validate()
}

func TestWorker_Run(t *testing.T) {
	worker := &external.Worker{}
	worker.Run()
}

func TestWorker_RunError(t *testing.T) { // want `test function "TestWorker_RunError" also tests Worker.Run; consolidate it into "TestWorker_Run" using table-driven cases or t.Run subtests`
	worker := &external.Worker{}
	worker.Run()
}

func TestHiddenWorker_Execute(t *testing.T) {
	worker := external.NewHiddenWorker()
	worker.Execute()
}
