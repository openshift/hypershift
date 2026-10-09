package crosspackage // want package:"testfuncstructure production symbols"

func Validate() {}

type Worker struct{}

func (*Worker) Run() {}

type Other struct{}

func (*Other) Run() {}
