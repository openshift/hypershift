package provenance

import "testing"

func ExportedTestHelper(t *testing.T) {}

type TestOnlyWorker struct{}

func (*TestOnlyWorker) Run() {}
