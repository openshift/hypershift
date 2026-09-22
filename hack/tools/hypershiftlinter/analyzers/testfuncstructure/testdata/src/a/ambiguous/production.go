package ambiguous

type First struct{}

func (*First) Reconcile() {}

type Second struct{}

func (*Second) Reconcile() {}
