package externallegacy

import "testing"

func TestValidateErrors(t *testing.T) {
	Validate()
}

func TestWorker_Run(t *testing.T) {
	worker := &Worker{}
	worker.Run()
}
