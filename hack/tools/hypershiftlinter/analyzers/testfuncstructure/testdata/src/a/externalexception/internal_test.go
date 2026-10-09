package externalexception

import "testing"

//nolint:hypershiftlinter // testfuncstructure: validates compatibility across package boundaries
func TestValidateCompatibility(t *testing.T) {
	Validate()
}

//nolint:hypershiftlinter // testfuncstructure: validates internal compatibility independently of the external contract
func TestWorker_Run(t *testing.T) {
	worker := &Worker{}
	worker.Run()
}
