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

// Valid: nested BeforeSuite with context.Background()
func TestNestedBeforeSuite(t *testing.T) {
	Describe("suite", func() {
		BeforeSuite(func() {
			ctx := context.Background()
			setup(ctx)
		})
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

// Valid: context.Background() inside AfterSuite is exempt
func TestAfterSuiteExempt(t *testing.T) {
	AfterSuite(func() {
		ctx := context.Background()
		cleanup(ctx)
	})
}

// Valid: context.Background() inside SynchronizedAfterSuite is exempt
func TestSynchronizedAfterSuiteExempt(t *testing.T) {
	SynchronizedAfterSuite(func() {
		ctx := context.Background()
		cleanup(ctx)
	})
}

// Test helpers
func BeforeSuite(f func())                {}
func AfterSuite(f func())                 {}
func SynchronizedBeforeSuite(f ...func()) {}
func SynchronizedAfterSuite(f ...func())  {}
func Describe(name string, f func())      {}
func setup(ctx context.Context)           {}
func cleanup(ctx context.Context)         {}
