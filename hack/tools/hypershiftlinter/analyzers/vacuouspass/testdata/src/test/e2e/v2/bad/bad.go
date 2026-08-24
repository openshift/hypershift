package bad

// Invalid: range over .Items without preceding assertion
var _ = Describe("Test", func() {
	It("checks items", func() {
		list := getList()
		for _, item := range list.Items { // want `range over \.Items without preceding non-empty assertion — add Expect\(x\.Items\)\.NotTo\(BeEmpty\(\)\) before the loop`
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Invalid: range without assertion in nested Context
var _ = Describe("Test", func() {
	Context("nested", func() {
		It("checks items", func() {
			list := getList()
			for _, item := range list.Items { // want `range over \.Items without preceding non-empty assertion — add Expect\(x\.Items\)\.NotTo\(BeEmpty\(\)\) before the loop`
				Expect(item.Name).NotTo(BeEmpty())
			}
		})
	})
})

// Invalid: multiple range loops, second one missing assertion
var _ = Describe("Test", func() {
	It("checks multiple lists", func() {
		list1 := getList()
		Expect(list1.Items).NotTo(BeEmpty())
		for _, item := range list1.Items {
			Expect(item.Name).NotTo(BeEmpty())
		}

		list2 := getList()
		for _, item := range list2.Items { // want `range over \.Items without preceding non-empty assertion — add Expect\(x\.Items\)\.NotTo\(BeEmpty\(\)\) before the loop`
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Invalid: assertion AFTER the range loop (not before)
var _ = Describe("Test", func() {
	It("checks items with late assertion", func() {
		list := getList()
		for _, item := range list.Items { // want `range over \.Items without preceding non-empty assertion — add Expect\(x\.Items\)\.NotTo\(BeEmpty\(\)\) before the loop`
			Expect(item.Name).NotTo(BeEmpty())
		}
		Expect(list.Items).NotTo(BeEmpty())
	})
})

// Invalid: When block with range loop missing assertion
var _ = Describe("Test", func() {
	When("condition is met", func() {
		It("checks items without assertion", func() {
			list := getList()
			for _, item := range list.Items { // want `range over \.Items without preceding non-empty assertion — add Expect\(x\.Items\)\.NotTo\(BeEmpty\(\)\) before the loop`
				Expect(item.Name).NotTo(BeEmpty())
			}
		})
	})
})

// Invalid: HaveLen(0) permits an empty list, so it is not a non-empty guard.
var _ = Describe("Test", func() {
	It("checks items with HaveLen(0)", func() {
		list := getList()
		Expect(list.Items).To(HaveLen(0))
		for _, item := range list.Items { // want `range over \.Items without preceding non-empty assertion — add Expect\(x\.Items\)\.NotTo\(BeEmpty\(\)\) before the loop`
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Invalid: BeNumerically("<", 1) permits an empty list.
var _ = Describe("Test", func() {
	It("checks items with BeNumerically less-than-one", func() {
		list := getList()
		Expect(len(list.Items)).To(BeNumerically("<", 1))
		for _, item := range list.Items { // want `range over \.Items without preceding non-empty assertion — add Expect\(x\.Items\)\.NotTo\(BeEmpty\(\)\) before the loop`
			Expect(item.Name).NotTo(BeEmpty())
		}
	})
})

// Invalid: BeNumerically("==", 0) permits (requires) an empty list.
var _ = Describe("Test", func() {
	It("checks items with BeNumerically equal-zero", func() {
		list := getList()
		Expect(len(list.Items)).To(BeNumerically("==", 0))
		for _, item := range list.Items { // want `range over \.Items without preceding non-empty assertion — add Expect\(x\.Items\)\.NotTo\(BeEmpty\(\)\) before the loop`
			Expect(item.Name).NotTo(BeEmpty())
		}
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
func Expect(val interface{}) Assertion    { return Assertion{} }

type Assertion struct{}

func (a Assertion) NotTo(matcher Matcher) {}
func (a Assertion) To(matcher Matcher)    {}

type Matcher struct{}

func BeEmpty() Matcher                                                  { return Matcher{} }
func HaveLen(count int) Matcher                                         { return Matcher{} }
func BeNumerically(comparator string, compareTo ...interface{}) Matcher { return Matcher{} }

func getList() ItemList {
	return ItemList{Items: []Item{{Name: "test"}}}
}
