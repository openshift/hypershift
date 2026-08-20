package pathutil

import "strings"

// IsUnitTest returns true for _test.go files that are NOT e2e or integration tests.
// TESTING.md conventions apply to unit tests only.
func IsUnitTest(filename string) bool {
	if !strings.HasSuffix(filename, "_test.go") {
		return false
	}
	return !strings.Contains(filename, "test/e2e/") &&
		!strings.Contains(filename, "test/integration/")
}

// IsV2E2ETest returns true for files under test/e2e/v2/.
// test/e2e/v2/AGENTS.md conventions apply to v2 e2e tests only.
func IsV2E2ETest(filename string) bool {
	return strings.Contains(filename, "test/e2e/v2/")
}
