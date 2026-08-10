package bad

import "testing"

func TestBadNames(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{
			name: "it works", // want `test case name "it works" must match format "When <condition>, it should <expected behavior>"`
			want: "ok",
		},
		{
			name: "should do X", // want `test case name "should do X" must match format "When <condition>, it should <expected behavior>"`
			want: "X",
		},
		{
			name: "When X should Y", // want `test case name "When X should Y" must match format "When <condition>, it should <expected behavior>"`
			want: "Y",
		},
		{
			name: "when x, it should y", // want `test case name "when x, it should y" must match format "When <condition>, it should <expected behavior>"`
			want: "y",
		},
		{
			name: "When X it should Y", // want `test case name "When X it should Y" must match format "When <condition>, it should <expected behavior>"`
			want: "Y",
		},
		{
			name: "happy path", // want `test case name "happy path" must match format "When <condition>, it should <expected behavior>"`
			want: "success",
		},
		{
			name: "test something", // want `test case name "test something" must match format "When <condition>, it should <expected behavior>"`
			want: "result",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// test implementation
		})
	}
}

func TestWithExpectedField(t *testing.T) {
	tests := []struct {
		name     string
		expected int
	}{
		{
			name:     "bad name here", // want `test case name "bad name here" must match format "When <condition>, it should <expected behavior>"`
			expected: 42,
		},
	}

	for _, tt := range tests {
		_ = tt
	}
}

func TestWithWantErrField(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "error case", // want `test case name "error case" must match format "When <condition>, it should <expected behavior>"`
			wantErr: true,
		},
	}

	for _, tt := range tests {
		_ = tt
	}
}

func TestMapBasedBadNames(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"it works": { // want `test case name "it works" must match format "When <condition>, it should <expected behavior>"`
			input: "a",
		},
		"does the right thing": { // want `test case name "does the right thing" must match format "When <condition>, it should <expected behavior>"`
			input: "b",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_ = tt
		})
	}
}

func TestWithSetupField(t *testing.T) {
	tests := []struct {
		name  string
		setup func()
	}{
		{
			name:  "missing format", // want `test case name "missing format" must match format "When <condition>, it should <expected behavior>"`
			setup: func() {},
		},
	}

	for _, tt := range tests {
		_ = tt
	}
}
