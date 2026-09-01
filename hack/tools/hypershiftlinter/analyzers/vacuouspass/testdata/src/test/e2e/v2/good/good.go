package good

// Valid: has Expect().NotTo(BeEmpty()) before range
var _ = Describe("Test", func() {
	It("checks items", func() {
		list := getList()
		Expect(list.Items).NotTo(BeEmpty())
		for _, item := range list.Items {
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Valid: BeforeEach assertion covers It block
var _ = Describe("Test", func() {
	BeforeEach(func() {
		list := getList()
		Expect(list.Items).NotTo(BeEmpty())
	})

	It("checks items", func() {
		list := getList()
		for _, item := range list.Items {
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Valid: range without Expect in body (no assertions)
var _ = Describe("Test", func() {
	It("processes items", func() {
		list := getList()
		for _, item := range list.Items {
			_ = item.Name
		}
	})
})

// Valid: range over non-.Items field
var _ = Describe("Test", func() {
	It("checks names", func() {
		names := []string{"a", "b"}
		for _, name := range names {
			Expect(name).NotTo(BeEmpty())
		}
	})
})

// Valid: When block with proper assertion before range
var _ = Describe("Test", func() {
	When("condition is met", func() {
		It("checks items", func() {
			list := getList()
			Expect(list.Items).NotTo(BeEmpty())
			for _, item := range list.Items {
				Expect(item.Name).NotTo(BeEmpty())
			}
		})
	})
})

// Valid: using ShouldNot matcher
var _ = Describe("Test", func() {
	It("checks items with ShouldNot", func() {
		list := getList()
		Expect(list.Items).ShouldNot(BeEmpty())
		for _, item := range list.Items {
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Valid: using ToNot matcher
var _ = Describe("Test", func() {
	It("checks items with ToNot", func() {
		list := getList()
		Expect(list.Items).ToNot(BeEmpty())
		for _, item := range list.Items {
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Valid: using HaveLen assertion before range
var _ = Describe("Test", func() {
	It("checks items with HaveLen", func() {
		list := getList()
		Expect(list.Items).To(HaveLen(3))
		for _, item := range list.Items {
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Valid: using BeNumerically with len() before range
var _ = Describe("Test", func() {
	It("checks items with BeNumerically", func() {
		list := getList()
		Expect(len(list.Items)).To(BeNumerically(">", 0))
		for _, item := range list.Items {
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Valid: BeNumerically(">=", 1) proves a positive length
var _ = Describe("Test", func() {
	It("checks items with BeNumerically at-least-one", func() {
		list := getList()
		Expect(len(list.Items)).To(BeNumerically(">=", 1))
		for _, item := range list.Items {
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Valid: BeNumerically("==", 3) proves a positive length
var _ = Describe("Test", func() {
	It("checks items with BeNumerically equal-three", func() {
		list := getList()
		Expect(len(list.Items)).To(BeNumerically("==", 3))
		for _, item := range list.Items {
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Valid: deep nesting — BeforeEach at Describe level covers It in Context > When > It
var _ = Describe("Test", func() {
	BeforeEach(func() {
		list := getList()
		Expect(list.Items).NotTo(BeEmpty())
	})

	Context("level 1", func() {
		When("level 2", func() {
			It("deeply nested item check", func() {
				list := getList()
				for _, item := range list.Items {
					Expect(item.Name).NotTo(BeEmpty())
				}
			})
		})
	})
})

// Test helpers
type ItemList struct {
	Items []Item
}

type Item struct {
	Name string
}

func Describe(name string, f func()) bool { return true }
func Context(name string, f func())       {}
func When(name string, f func())          {}
func It(name string, f func())            {}
func BeforeEach(f func())                 {}
func Expect(val interface{}) Assertion    { return Assertion{} }

type Assertion struct{}

func (a Assertion) NotTo(matcher Matcher)     {}
func (a Assertion) ShouldNot(matcher Matcher) {}
func (a Assertion) ToNot(matcher Matcher)     {}
func (a Assertion) To(matcher Matcher)        {}
func (a Assertion) Should(matcher Matcher)    {}

type Matcher struct{}

func BeEmpty() Matcher                                                  { return Matcher{} }
func HaveLen(count int) Matcher                                         { return Matcher{} }
func BeNumerically(comparator string, compareTo ...interface{}) Matcher { return Matcher{} }

func getList() ItemList {
	return ItemList{Items: []Item{{Name: "test"}}}
}
