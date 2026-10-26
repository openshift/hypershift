package dump

func VerifyAzureMachineDiagnostics() {}

type Diagnostics struct{ Message string }

func (*Diagnostics) Collect() {}
