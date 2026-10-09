package externalexception_test

import (
	"testing"

	"a/externalexception"
)

func TestValidate(t *testing.T) {
	externalexception.Validate()
}

func TestWorker_Run(t *testing.T) {
	worker := &externalexception.Worker{}
	worker.Run()
}

func TestWorkerRun(t *testing.T) { // want `test function "TestWorkerRun" duplicates "TestWorker_Run" for Worker.Run; consolidate scenarios under "TestWorker_Run" using table-driven cases or t.Run subtests`
	worker := &externalexception.Worker{}
	worker.Run()
}
