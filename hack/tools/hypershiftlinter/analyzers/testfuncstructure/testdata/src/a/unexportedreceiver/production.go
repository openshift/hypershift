package unexportedreceiver // want package:"testfuncstructure production symbols"

type worker struct{}

func NewWorker() *worker {
	return &worker{}
}

func (*worker) Run() {}
