//go:build e2ev2

package internal

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
)

// InformingLabel is the Ginkgo label that marks a test as informing.
const InformingLabel = "Informing"

const informingSkipPrefix = "informing test failure: "

// InformingAwareFailHandler checks if the current spec has the "Informing" label.
// If so, it skips the test with the failure message instead of failing the suite.
func InformingAwareFailHandler(message string, callerSkip ...int) {
	labels := CurrentSpecReport().Labels()
	if slices.Contains(labels, InformingLabel) {
		Skip(informingSkipPrefix+message, callerSkip...)
	}
	Fail(message, callerSkip...)
}

// IsInformingFailureSkip returns true if the spec was skipped by
// InformingAwareFailHandler due to an informing test failure.
func IsInformingFailureSkip(spec types.SpecReport) bool {
	return spec.State == types.SpecStateSkipped &&
		slices.Contains(spec.Labels(), InformingLabel) &&
		strings.HasPrefix(spec.Failure.Message, informingSkipPrefix)
}

// InformingFailureMessage extracts the original failure message from a spec
// that was skipped by InformingAwareFailHandler.
func InformingFailureMessage(spec types.SpecReport) string {
	return strings.TrimPrefix(spec.Failure.Message, informingSkipPrefix)
}
