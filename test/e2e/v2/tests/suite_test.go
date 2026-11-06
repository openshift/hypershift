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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	appsv1 "k8s.io/api/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
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
	suiteConfig, reporterConfig := GinkgoConfiguration()
	junitReportPath, reporterConfig = internal.ConfigureJUnitReport(junitReportPath, reporterConfig)

	// Register fail handler with gomega
	RegisterFailHandler(internal.InformingAwareFailHandler)

	// Run the ginkgo test suite
	RunSpecs(t, "hypershift-e2e", suiteConfig, reporterConfig)
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

	// EXPERIMENT (main-CPO-on-4.20): log runtime evidence from the LIVE cluster that the
	// guest is pinned to an old release while the CPO is the PR build. This reads actual
	// cluster state (not just intent): the HC spec release, its desired version, and the
	// real control-plane-operator deployment container image. Best-effort; never fails the
	// suite. Uses GinkgoWriter so output is captured by the test framework.
	if testCtx.ClusterName != "" && testCtx.ClusterNamespace != "" {
		// Use a short child context so a stalled management API cannot delay BeforeSuite by
		// the full transport timeout. Do not replace testCtx.Context, which the suite shares.
		diagCtx, diagCancel := context.WithTimeout(testCtx.Context, 30*time.Second)
		defer diagCancel()
		hc := &hyperv1.HostedCluster{}
		hcErr := testCtx.MgmtClient.Get(diagCtx, crclient.ObjectKey{Namespace: testCtx.ClusterNamespace, Name: testCtx.ClusterName}, hc)
		if hcErr != nil {
			fmt.Fprintf(GinkgoWriter, "EXPERIMENT main-CPO-on-4.20 [v2 BeforeSuite]: could not read HostedCluster for logging: %v\n", hcErr)
		} else {
			desiredVersion := "<unknown>"
			if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 {
				desiredVersion = hc.Status.Version.History[0].Version
			}
			cpoAnnotation := hc.Annotations[hyperv1.ControlPlaneOperatorImageAnnotation]
			fmt.Fprintf(GinkgoWriter, "===== EXPERIMENT main-CPO-on-4.20 RUNTIME EVIDENCE [v2 BeforeSuite] =====\n")
			fmt.Fprintf(GinkgoWriter, "  HostedCluster: %s/%s\n", hc.Namespace, hc.Name)
			fmt.Fprintf(GinkgoWriter, "  HC spec.release.image (guest):        %s\n", hc.Spec.Release.Image)
			fmt.Fprintf(GinkgoWriter, "  HC status desired/current version:    %s\n", desiredVersion)
			fmt.Fprintf(GinkgoWriter, "  HC control-plane-operator-image annotation (CPO from PR): %s\n", cpoAnnotation)

			cpoDeploy := &appsv1.Deployment{}
			dErr := testCtx.MgmtClient.Get(diagCtx, crclient.ObjectKey{Namespace: testCtx.ControlPlaneNamespace, Name: "control-plane-operator"}, cpoDeploy)
			if dErr != nil {
				fmt.Fprintf(GinkgoWriter, "  deployed CPO image: <could not read control-plane-operator deployment in %s: %v>\n", testCtx.ControlPlaneNamespace, dErr)
			} else {
				for _, c := range cpoDeploy.Spec.Template.Spec.Containers {
					if c.Name == "control-plane-operator" {
						fmt.Fprintf(GinkgoWriter, "  deployed CPO image (live, from PR payload): %s\n", c.Image)
					}
				}
			}
			fmt.Fprintf(GinkgoWriter, "========================================================================\n")
		}
	}
})
