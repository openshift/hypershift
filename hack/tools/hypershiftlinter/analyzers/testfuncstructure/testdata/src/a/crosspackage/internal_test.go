package crosspackage

import "testing"

func TestValidate(t *testing.T) {
	Validate()
}

func TestRun(t *testing.T) {
	worker := &Worker{}
	worker.Run()
}
