package pathutil

import "testing"

func TestIsUnitTest(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{name: "When file is a unit test, it should return true", filename: "pkg/foo_test.go", want: true},
		{name: "When file is in a nested package, it should return true", filename: "hypershift-operator/controllers/nodepool/nodepool_controller_test.go", want: true},
		{name: "When file is not a test, it should return false", filename: "pkg/foo.go", want: false},
		{name: "When file is an e2e test, it should return false", filename: "test/e2e/util/util_test.go", want: false},
		{name: "When file is a v2 e2e test, it should return false", filename: "test/e2e/v2/smoke_test.go", want: false},
		{name: "When file is an integration test, it should return false", filename: "test/integration/foo_test.go", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUnitTest(tt.filename); got != tt.want {
				t.Errorf("IsUnitTest(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestIsV2E2ETest(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{name: "When file is under test/e2e/v2, it should return true", filename: "test/e2e/v2/smoke_test.go", want: true},
		{name: "When file is in a v2 subdirectory, it should return true", filename: "test/e2e/v2/nodepool/nodepool_test.go", want: true},
		{name: "When file is a non-test file under v2, it should return true", filename: "test/e2e/v2/helpers.go", want: true},
		{name: "When file is under test/e2e but not v2, it should return false", filename: "test/e2e/util/util_test.go", want: false},
		{name: "When file is a regular package, it should return false", filename: "pkg/foo.go", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsV2E2ETest(tt.filename); got != tt.want {
				t.Errorf("IsV2E2ETest(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}
