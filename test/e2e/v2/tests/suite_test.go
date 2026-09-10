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
	"flag"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/internal"
	ctrl "sigs.k8s.io/controller-runtime"
	zap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var junitReportPath string

func init() {
	flag.StringVar(&junitReportPath, "e2e.junit-report", "", "path to the v2 E2E JUnit XML report")
}

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
	_, reporterConfig := GinkgoConfiguration()
	if reporterConfig.JUnitReport != "" {
		t.Fatal("--ginkgo.junit-report bypasses the v2 E2E JUnit contract; use --e2e.junit-report instead")
	}

	// Register fail handler with gomega
	RegisterFailHandler(internal.InformingAwareFailHandler)

	// Run the ginkgo test suite
	RunSpecs(t, "hypershift-e2e")
}

// ReportAfterSuite writes the unified v2 E2E JUnit report. Informing tests have
// lifecycle="informing", and their assertion failures are reported as failures
// without causing the suite itself to fail. This is picked up by ci-to-bigquery
// and loaded into the ci_analysis_us.junit BigQuery table, making informing test
// failures visible to Component Readiness.
//
// TODO(CNTRLPLANE-3863): Replace this with OTE's built-in lifecycle JUnit
// emission once the test framework is ported to OTE.
var _ = ReportAfterSuite("Write unified JUnit", func(report Report) {
	if junitReportPath == "" {
		return
	}
	if err := internal.GenerateJUnitReport(report, junitReportPath); err != nil {
		Fail(fmt.Sprintf("failed to write JUnit report to %s: %v", junitReportPath, err))
	}
})

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
