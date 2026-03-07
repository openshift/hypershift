//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/internal"
	ctrl "sigs.k8s.io/controller-runtime"
	zap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// TestShowEnvHelp is a convenience test that prints environment variable help.
// Run with: go test -v ./test/e2e/v2/tests -run TestShowEnvHelp
func TestShowEnvHelp(t *testing.T) {
	internal.PrintEnvVarHelp()
}

func TestE2EV2(t *testing.T) {
	// Check if help is requested via environment variable
	// Usage: E2E_SHOW_ENV_HELP=1 go test ./test/e2e/v2/tests
	if internal.GetEnvVarValue("E2E_SHOW_ENV_HELP") != "" {
		internal.PrintEnvVarHelp()
		return
	}

	// Register fail handler with gomega
	RegisterFailHandler(internal.InformingAwareFailHandler)

	// Run the ginkgo test suite
	RunSpecs(t, "HyperShift End To End Test Suite")
}

var _ = BeforeSuite(func() {
	ctx := context.Background()

	ctrl.SetLogger(zap.New())

	// Setup test context from environment variables
	// This creates a common context for all tests
	testCtx, err := internal.SetupTestContextFromEnv(ctx)
	Expect(err).NotTo(HaveOccurred(), "failed to setup test context")
	Expect(testCtx).NotTo(BeNil(), "test context should not be nil")

	// Set the global test context
	internal.SetTestContext(testCtx)
})
