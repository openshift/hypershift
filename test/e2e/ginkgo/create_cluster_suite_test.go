//go:build e2e
// +build e2e

package ginkgo

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

var (
	// Global test context and options initialized in BeforeSuite
	testContext context.Context
	globalOpts  *e2eutil.Options
)

func TestCreateClusterGinkgo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CreateCluster Ginkgo Suite")
}

var _ = BeforeSuite(func() {
	// Initialize options
	// TODO: This is a simplified initialization for the pilot migration
	// Full flag parsing can be added in future iterations
	globalOpts = &e2eutil.Options{}
	// Set defaults - actual values will come from test invocation flags
	testContext = context.Background()

	// Note: Ginkgo will handle flag parsing, and test invocations will need to
	// pass the appropriate e2e.* flags
})
