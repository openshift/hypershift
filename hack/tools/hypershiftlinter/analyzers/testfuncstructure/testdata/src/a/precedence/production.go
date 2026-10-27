package precedence // want package:"testfuncstructure production symbols"

func Reconcile() {}

type Controller struct{}

func (*Controller) Reconcile() {}
