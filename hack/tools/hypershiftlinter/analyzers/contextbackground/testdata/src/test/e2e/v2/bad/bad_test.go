package bad

import (
	"context"
	"testing"
)

// Invalid: using context.Background() in regular test
func TestBadUsage(t *testing.T) {
	ctx := context.Background() // want `use tc\.Context instead of context\.Background\(\)/context\.TODO\(\)`
	_ = ctx
}

// Invalid: context.Background() in It block
func TestItBlock(t *testing.T) {
	It("does something", func() {
		ctx := context.Background() // want `use tc\.Context instead of context\.Background\(\)/context\.TODO\(\)`
		_ = ctx
	})
}

// Invalid: context.Background() in helper function
func helperFunction() {
	ctx := context.Background() // want `use tc\.Context instead of context\.Background\(\)/context\.TODO\(\)`
	_ = ctx
}

// Invalid: context.Background() in AfterEach (not exempt, only BeforeSuite and DeferCleanup are)
func TestAfterEachNotExempt(t *testing.T) {
	AfterEach(func() {
		ctx := context.Background() // want `use tc\.Context instead of context\.Background\(\)/context\.TODO\(\)`
		_ = ctx
	})
}

// Invalid: context.TODO() in It block
func TestTODOInItBlock(t *testing.T) {
	It("does something with TODO", func() {
		ctx := context.TODO() // want `use tc\.Context instead of context\.Background\(\)/context\.TODO\(\)`
		_ = ctx
	})
}

// Invalid: context.Background() in BeforeEach (not exempt, only suite-level hooks are)
func TestBeforeEachNotExempt(t *testing.T) {
	BeforeEach(func() {
		ctx := context.Background() // want `use tc\.Context instead of context\.Background\(\)/context\.TODO\(\)`
		_ = ctx
	})
}

// Invalid: context.Background() in DeferCleanup is NOT exempt — TestContext.Context
// is initialized once in BeforeSuite and not canceled during cleanup, so cleanup
// callbacks must use tc.Context.
func TestDeferCleanupNotExempt(t *testing.T) {
	DeferCleanup(func() {
		ctx := context.Background() // want `use tc\.Context instead of context\.Background\(\)/context\.TODO\(\)`
		_ = ctx
	})
}

// Invalid: context.TODO() in DeferCleanup where tc is directly available.
func TestDeferCleanupWithTC(t *testing.T) {
	It("does something", func() {
		DeferCleanup(func() {
			ctx := context.TODO() // want `use tc\.Context instead of context\.Background\(\)/context\.TODO\(\)`
			cleanup(ctx)
		})
	})
}

// Test helpers
func It(desc string, f func())    {}
func AfterEach(f func())          {}
func BeforeEach(f func())         {}
func DeferCleanup(f func())       {}
func cleanup(ctx context.Context) {}
