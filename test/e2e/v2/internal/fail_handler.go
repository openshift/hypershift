//go:build e2ev2

package internal

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
)

// InformingAwareFailHandler checks if the current spec has the "Informing" label.
// If so, it skips the test with the failure message instead of failing the suite.
func InformingAwareFailHandler(message string, callerSkip ...int) {
	labels := CurrentSpecReport().Labels()
	if slices.Contains(labels, "Informing") {
		// Skip marks test as skipped (visible in reports) without failing suite
		Skip("informing test failure: " + message, callerSkip...)
	}
	// For non-Informing tests, fail normally
	Fail(message, callerSkip...)
}
