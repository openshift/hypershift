package unexportedreceiver_test

import (
	"testing"

	"a/unexportedreceiver"
)

func TestWorker_Run(t *testing.T) {
	unexportedreceiver.NewWorker().Run()
}

func TestWorkerRun(t *testing.T) { // want `test function "TestWorkerRun" duplicates "TestWorker_Run" for worker.Run; consolidate scenarios under "TestWorker_Run" using table-driven cases or t.Run subtests`
	unexportedreceiver.NewWorker().Run()
}
