package provenance_test

import (
	"testing"

	"a/provenance"
)

func TestValidate(t *testing.T) {
	provenance.Validate()
}

func TestGeneratedBehavior(t *testing.T) {
	widget := &provenance.Widget{}
	widget.DeepCopy()
}

func TestHelperBehavior(t *testing.T) {
	provenance.ExportedTestHelper(t)
}

func TestHelperMethodBehavior(t *testing.T) {
	worker := &provenance.TestOnlyWorker{}
	worker.Run()
}
