package bad

import d "github.com/openshift/hypershift/test/e2e/util/dump"

func subpackage() {
	d.VerifyAzureMachineDiagnostics()       // want `reference to .*util/dump.VerifyAzureMachineDiagnostics is not allowed`
	value := d.Diagnostics{Message: "test"} // want `reference to .*util/dump.Diagnostics is not allowed` `reference to .*util/dump.Message is not allowed`
	value.Collect()                         // want `reference to .*util/dump.Collect is not allowed`
	_ = value.Message                       // want `reference to .*util/dump.Message is not allowed`
}
