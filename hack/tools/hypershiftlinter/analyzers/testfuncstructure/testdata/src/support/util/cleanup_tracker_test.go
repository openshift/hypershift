package util

import "testing"

func TestConcurrentAccess(t *testing.T) { // want `test function "TestConcurrentAccess" must be named "TestRecordFailure" to map to RecordFailure`
	RecordFailure()
}
