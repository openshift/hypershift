package dump

func VerifyAzureMachineDiagnostics() {}

type Diagnostics struct{ Message string }

func (*Diagnostics) Collect() {}

type Other struct{ Message string }

func (*Other) Collect() {}

type Embedded struct{ Diagnostics }
type Alias = Diagnostics
