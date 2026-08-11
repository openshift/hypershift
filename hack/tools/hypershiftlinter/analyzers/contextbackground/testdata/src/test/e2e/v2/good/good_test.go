package good

import (
	"context"
	"testing"
)

// Valid: context.Background() inside BeforeSuite is exempt
func TestBeforeSuiteExempt(t *testing.T) {
	BeforeSuite(func() {
		ctx := context.Background()
		_ = ctx
	})
}

// Valid: context.Background() inside DeferCleanup is exempt
func TestDeferCleanupExempt(t *testing.T) {
	DeferCleanup(func() {
		ctx := context.Background()
		_ = ctx
	})
}

// Valid: nested BeforeSuite with context.Background()
func TestNestedBeforeSuite(t *testing.T) {
	Describe("suite", func() {
		BeforeSuite(func() {
			ctx := context.Background()
			setup(ctx)
		})
	})
}

// Valid: DeferCleanup in cleanup chain
func TestDeferCleanupChain(t *testing.T) {
	DeferCleanup(func() {
		ctx := context.Background()
		cleanup(ctx)
	})
}

// Valid: multiple context.Background() calls in BeforeSuite
func TestMultipleBackgroundInBeforeSuite(t *testing.T) {
	BeforeSuite(func() {
		ctx1 := context.Background()
		setup(ctx1)
		ctx2 := context.Background()
		setup(ctx2)
	})
}

// Valid: context.Background() inside SynchronizedBeforeSuite is exempt
func TestSynchronizedBeforeSuiteExempt(t *testing.T) {
	SynchronizedBeforeSuite(func() {
		ctx := context.Background()
		setup(ctx)
	})
}

// Valid: context.TODO() inside BeforeSuite is exempt
func TestTODOInBeforeSuiteExempt(t *testing.T) {
	BeforeSuite(func() {
		ctx := context.TODO()
		setup(ctx)
	})
}

// Test helpers
func BeforeSuite(f func())                {}
func DeferCleanup(f func())               {}
func SynchronizedBeforeSuite(f ...func()) {}
func SynchronizedAfterSuite(f ...func())  {}
func Describe(name string, f func())      {}
func setup(ctx context.Context)           {}
func cleanup(ctx context.Context)         {}
