package split // want package:"testfuncstructure production symbols"

func Reconcile() {}

func ReconcileErrors() {}

type Controller struct{}

func (*Controller) Sync() {}
