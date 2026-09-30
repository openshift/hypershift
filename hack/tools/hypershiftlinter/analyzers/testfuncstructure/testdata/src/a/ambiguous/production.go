package ambiguous // want package:"testfuncstructure production symbols"

type First struct{}

func (*First) Reconcile() {}

type Second struct{}

func (*Second) Reconcile() {}
