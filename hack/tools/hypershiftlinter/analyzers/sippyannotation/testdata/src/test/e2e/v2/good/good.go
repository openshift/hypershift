package good

// Valid: Describe with proper prefix and Feature annotation
var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:NodePool] NodePool tests", func() {
	It("should create a node pool", func() {})
})

// Valid: Describe with prefix and Feature in child Context
var _ = Describe("[sig-hypershift][Jira:Hypershift] Complex suite", func() {
	Context("[Feature:ControlPlane]", func() {
		It("should start control plane", func() {})
	})
})

// Valid: Describe with prefix and Feature in child When
var _ = Describe("[sig-hypershift][Jira:Hypershift] Advanced suite", func() {
	When("[Feature:Upgrade]", func() {
		It("should upgrade successfully", func() {})
	})
})

// Valid: Describe with Register*Tests call (exempt from Feature check)
var _ = Describe("[sig-hypershift][Jira:Hypershift] Test suite", func() {
	RegisterNodePoolTests()
})

// Valid: AssignStmt form with proper annotations
func TestAssignStmt() {
	describeResult := Describe("[sig-hypershift][Jira:Hypershift][Feature:Test] assign form", func() {
		It("should work", func() {})
	})
	_ = describeResult
}

// Valid: qualified ginkgo.Describe with proper prefix and Feature annotation
var _ = ginkgo.Describe("[sig-hypershift][Jira:Hypershift][Feature:Qualified] Qualified call", func() {
	It("should work", func() {})
})

// Valid: Describe with mixed children — some have Feature and some don't (at-least-one semantics)
var _ = Describe("[sig-hypershift][Jira:Hypershift] Mixed children suite", func() {
	Context("[Feature:MixedA] first context", func() {
		It("should work", func() {})
	})
	Context("no feature here", func() {
		It("should also work", func() {})
	})
})

// Test helpers
var ginkgo ginkgoT

type ginkgoT struct{}

func (ginkgoT) Describe(name string, f func()) bool { return true }
func Describe(name string, f func()) bool           { return true }
func Context(name string, f func())                 {}
func When(name string, f func())                    {}
func It(name string, f func())                      {}
func RegisterNodePoolTests()                        {}
