package good

func tests() {
	It("starts informing", Label(internal.InformingLabel), func() {})
	ginkgo.It("is explicitly blocking", ginkgo.Label(internal.BlockingLabel), func() {})
	It("may have other labels", Label("AWS", internal.BlockingLabel), func() {})
	Specify("specifies behavior", Label(internal.InformingLabel), func() {})
	FIt("focuses a test", Label(internal.InformingLabel), func() {})
	FSpecify("focuses specified behavior", Label(internal.BlockingLabel), func() {})
	PIt("pends a test", Label(internal.InformingLabel), func() {})
	PSpecify("pends specified behavior", Label(internal.BlockingLabel), func() {})
	XIt("disables a test", Label(internal.InformingLabel), func() {})
	XSpecify("disables specified behavior", Label(internal.BlockingLabel), func() {})
}

var internal labels
var ginkgo dsl

type labels struct {
	InformingLabel string
	BlockingLabel  string
}

type dsl struct{}

func (dsl) It(string, ...any)   {}
func (dsl) Label(...string) any { return nil }
func It(string, ...any)         {}
func Specify(string, ...any)    {}
func FIt(string, ...any)        {}
func FSpecify(string, ...any)   {}
func PIt(string, ...any)        {}
func PSpecify(string, ...any)   {}
func XIt(string, ...any)        {}
func XSpecify(string, ...any)   {}
func Label(...string) any       { return nil }
