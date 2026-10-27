package identity // want package:"testfuncstructure production symbols"

func Run() {}

type Box[T any] struct{}

func (Box[T]) Execute() {}
