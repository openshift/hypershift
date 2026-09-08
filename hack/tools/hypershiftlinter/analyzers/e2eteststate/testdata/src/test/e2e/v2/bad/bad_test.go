package bad

func tests() {
	It("has no state", func() {})                                                                   // want `Ginkgo subject must have Label\(internal.InformingLabel\) or Label\(internal.BlockingLabel\); new tests must be informing`
	It("has only an unrelated label", Label("AWS"), func() {})                                      // want `Ginkgo subject must have Label\(internal.InformingLabel\) or Label\(internal.BlockingLabel\); new tests must be informing`
	It("uses a raw state string", Label("Informing"), func() {})                                    // want `Ginkgo subject state labels must use internal.InformingLabel or internal.BlockingLabel instead of string literals`
	It("mixes constant and literal states", Label(internal.InformingLabel, "Blocking"), func() {})  // want `Ginkgo subject state labels must use internal.InformingLabel or internal.BlockingLabel instead of string literals`
	It("has conflicting states", Label(internal.InformingLabel, internal.BlockingLabel), func() {}) // want `Ginkgo subject must not have both internal.InformingLabel and internal.BlockingLabel`
	Specify("has no state", func() {})                                                              // want `Ginkgo subject must have Label\(internal.InformingLabel\) or Label\(internal.BlockingLabel\); new tests must be informing`
	FIt("has no state", func() {})                                                                  // want `Ginkgo subject must have Label\(internal.InformingLabel\) or Label\(internal.BlockingLabel\); new tests must be informing`
	FSpecify("has no state", func() {})                                                             // want `Ginkgo subject must have Label\(internal.InformingLabel\) or Label\(internal.BlockingLabel\); new tests must be informing`
	PIt("has no state", func() {})                                                                  // want `Ginkgo subject must have Label\(internal.InformingLabel\) or Label\(internal.BlockingLabel\); new tests must be informing`
	PSpecify("has no state", func() {})                                                             // want `Ginkgo subject must have Label\(internal.InformingLabel\) or Label\(internal.BlockingLabel\); new tests must be informing`
	XIt("has no state", func() {})                                                                  // want `Ginkgo subject must have Label\(internal.InformingLabel\) or Label\(internal.BlockingLabel\); new tests must be informing`
	XSpecify("has no state", func() {})                                                             // want `Ginkgo subject must have Label\(internal.InformingLabel\) or Label\(internal.BlockingLabel\); new tests must be informing`
	Describe("inherits informing", Label(internal.InformingLabel), func() {})                       // want `internal.InformingLabel and internal.BlockingLabel may only be applied directly to Ginkgo subject nodes`
	Context("inherits blocking", Label(internal.BlockingLabel), func() {})                          // want `internal.InformingLabel and internal.BlockingLabel may only be applied directly to Ginkgo subject nodes`
	PWhen("inherits informing", Label(internal.InformingLabel), func() {})                          // want `internal.InformingLabel and internal.BlockingLabel may only be applied directly to Ginkgo subject nodes`
	Describe("inherits a raw state", Label("Blocking"), func() {})                                  // want `internal.InformingLabel and internal.BlockingLabel may only be applied directly to Ginkgo subject nodes`
}

var internal labels

type labels struct {
	InformingLabel string
	BlockingLabel  string
}

func It(string, ...any)       {}
func Specify(string, ...any)  {}
func FIt(string, ...any)      {}
func FSpecify(string, ...any) {}
func PIt(string, ...any)      {}
func PSpecify(string, ...any) {}
func XIt(string, ...any)      {}
func XSpecify(string, ...any) {}
func Describe(string, ...any) {}
func Context(string, ...any)  {}
func PWhen(string, ...any)    {}
func Label(...string) any     { return nil }
