package external // want package:"testfuncstructure production symbols"

func Validate() {}

type Worker struct{}

func (*Worker) Run() {}

func hidden() {}

type hiddenWorker struct{}

func NewHiddenWorker() *hiddenWorker {
	return &hiddenWorker{}
}

func (*hiddenWorker) Execute() {}
