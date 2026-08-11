package good

import "testing"

func TestSomething(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "When X is set, it should return Y",
			want: "Y",
		},
		{
			name: "when x, it should y",
			want: "y",
		},
		{
			name: "When the user provides valid input, it should succeed",
			want: "success",
		},
		{
			name: "WHEN something happens, IT SHOULD respond",
			want: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// test implementation
		})
	}
}

func TestStructWithoutNameField(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "foo",
			expected: "bar",
		},
	}

	for _, tt := range tests {
		// test implementation
		_ = tt
	}
}

func TestNonTestStruct(t *testing.T) {
	// This struct doesn't look like a test case (no test fields)
	// so it should be skipped even if name doesn't match pattern
	type Config struct {
		name string
		host string
	}

	c := Config{
		name: "not a test case",
		host: "localhost",
	}
	_ = c
}

func TestNamedStructTypeDirect(t *testing.T) {
	// Direct use of named struct type - the analyzer skips these
	// because comp.Type would be *ast.Ident (not *ast.StructType)
	type testCase struct {
		name string
		want int
	}

	tc := testCase{
		name: "not checked because this uses named type directly",
		want: 42,
	}
	_ = tc
}

func TestNoCommaInName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "When X it should Y",
			want: "Y",
		},
		{
			name: "WHEN the condition is met IT SHOULD work",
			want: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// test implementation
		})
	}
}

func TestMapBasedGoodNames(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"When input is valid, it should succeed": {
			input: "a",
		},
		"When nothing is provided it should use defaults": {
			input: "",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_ = tt
		})
	}
}

func TestNameFromVariable(t *testing.T) {
	testName := "some dynamic name"
	tests := []struct {
		name string
		want string
	}{
		{
			name: testName,
			want: "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt
		})
	}
}

func TestWithValidateAndCheckFields(t *testing.T) {
	tests := []struct {
		name     string
		validate func() bool
		check    func() error
	}{
		{
			name:     "When fields include validate and check, it should pass",
			validate: func() bool { return true },
			check:    func() error { return nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt
		})
	}
}
