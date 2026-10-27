package identity

import "testing"

func TestAnonymousInterfaceCall(t *testing.T) {
	var runner interface{ Run() }
	runner.Run()
}

func TestGenericMethodCall(t *testing.T) { // want `test function "TestGenericMethodCall" must be named "TestBox_Execute" to map to Box.Execute`
	Box[int]{}.Execute()
}
