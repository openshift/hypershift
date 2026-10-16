package exception

import "testing"

//nolint:hypershiftlinter // testfuncstructure: validates compatibility independently of the standard method test
func TestWorker_Run(t *testing.T) {
	worker := &Worker{}
	worker.Run()
}

func TestWorkerRun(t *testing.T) {
	worker := &Worker{}
	worker.Run()
}

func TestRun(t *testing.T) { // want `test function "TestRun" duplicates "TestWorkerRun" for Worker.Run; consolidate scenarios under "TestWorkerRun" using table-driven cases or t.Run subtests`
	worker := &Worker{}
	worker.Run()
}

//nolint:hypershiftlinter // testfuncstructure: validates compatibility without an ordinary unit test
func TestValidate(t *testing.T) {
	Validate()
}
