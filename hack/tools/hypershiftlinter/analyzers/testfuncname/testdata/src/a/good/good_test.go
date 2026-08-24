package good

import (
	"os"
	"testing"
)

// TestMain is a special Go test function that must be allowed
func TestMain(m *testing.M) {
	// setup
	code := m.Run()
	// teardown
	os.Exit(code)
}

// Good test function names (no underscore after Test)
func TestFoo(t *testing.T) {
	// test implementation
}

func TestBarBaz(t *testing.T) {
	// test implementation
}

func TestSomethingElse(t *testing.T) {
	// test implementation
}

// Benchmark functions are not checked
func BenchmarkX(b *testing.B) {
	// benchmark implementation
}

// Example functions are not checked
func ExampleFoo() {
	// example implementation
}

// Methods with receivers are not checked (not top-level test functions)
type Suite struct{}

func (s *Suite) Test_methodName(t *testing.T) {
	// This is a method, not a top-level function, so it's allowed
}

// Helper functions don't start with Test
func helperFunction(t *testing.T) {
	// helper implementation
}

// Underscores in subtest names (t.Run parameter) are fine
func TestSubtestWithUnderscores(t *testing.T) {
	t.Run("test_name_with_underscores", func(t *testing.T) {
		// The underscore is in the subtest name string, not the function name
	})

	t.Run("another_test_case", func(t *testing.T) {
		// This is also fine
	})
}
