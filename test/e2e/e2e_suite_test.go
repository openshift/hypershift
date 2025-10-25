//go:build e2e

package e2e

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestE2E bootstraps the Ginkgo test suite for HyperShift E2E tests.
// The actual test infrastructure setup (options, signal handling, OIDC, etc.)
// is handled by TestMain in e2e_test.go.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "hypershift-e2e")
}
