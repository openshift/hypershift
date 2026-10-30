package good

func Reconcile() {}

func buildThing() {}

func ReconcileLonger() {}

type Controller struct{}

func (*Controller) Sync() {}

func (*Controller) reconcile() {}

type OtherController struct{}

func (*OtherController) Sync() {}

type Starter struct{}

func (*Starter) Start() {}

type worker struct{}

func (*worker) Run() {}
