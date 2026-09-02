package bad

// Invalid: missing required prefix
var _ = Describe("NodePool tests", func() { // want `Describe block must start with \[sig-hypershift\]\[Jira:Hypershift\]`
	It("should work", func() {})
})

// Invalid: has prefix but missing Feature annotation
var _ = Describe("[sig-hypershift][Jira:Hypershift] Missing feature", func() { // want `Describe block has no \[Feature:X\] annotation — add to Describe or to every child Context/When`
	It("should work", func() {})
})

// Invalid: wrong prefix format
var _ = Describe("[Jira:Hypershift][sig-hypershift] Wrong order", func() { // want `Describe block must start with \[sig-hypershift\]\[Jira:Hypershift\]`
	It("should work", func() {})
})

// Invalid: has prefix but no Feature and no children
var _ = Describe("[sig-hypershift][Jira:Hypershift] No children no feature", func() { // want `Describe block has no \[Feature:X\] annotation — add to Describe or to every child Context/When`
})

// Invalid: Feature annotation with empty value
var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:] Empty feature", func() { // want `Describe block has no \[Feature:X\] annotation — add to Describe or to every child Context/When`
	It("should work", func() {})
})

// Invalid: qualified ginkgo.Describe with bad prefix
var _ = ginkgo.Describe("Qualified bad prefix", func() { // want `Describe block must start with \[sig-hypershift\]\[Jira:Hypershift\]`
	It("should work", func() {})
})

// Invalid: bare ExprStmt Describe with bad prefix
func init() {
	Describe("ExprStmt bad prefix", func() { // want `Describe block must start with \[sig-hypershift\]\[Jira:Hypershift\]`
		It("should work", func() {})
	})
}

// Test helpers
var ginkgo ginkgoT

type ginkgoT struct{}

func (ginkgoT) Describe(name string, f func()) bool { return true }
func Describe(name string, f func()) bool           { return true }
func It(name string, f func())                      {}
