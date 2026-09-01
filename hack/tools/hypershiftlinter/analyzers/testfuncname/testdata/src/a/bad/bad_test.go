package bad

import "testing"

func Test_foo(t *testing.T) { // want `test function "Test_foo" must not use Test_ prefix; use Testfoo`
	// test implementation
}

func Test_bar_baz(t *testing.T) { // want `test function "Test_bar_baz" must not use Test_ prefix; use Testbar_baz`
	// test implementation
}

func Test_something_else(t *testing.T) { // want `test function "Test_something_else" must not use Test_ prefix; use Testsomething_else`
	// test implementation
}

func Test_WithMultipleWords(t *testing.T) { // want `test function "Test_WithMultipleWords" must not use Test_ prefix; use TestWithMultipleWords`
	// test implementation
}
