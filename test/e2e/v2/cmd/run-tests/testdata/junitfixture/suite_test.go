//go:build e2ev2

package junitfixture

import (
	"flag"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/internal"
)

var junitReportPath string

func init() {
	flag.StringVar(&junitReportPath, "e2e.junit-report", "", "path to the synthetic JUnit XML report")
}

func TestJUnitFixture(t *testing.T) {
	RegisterFailHandler(internal.InformingAwareFailHandler)
	RunSpecs(t, "synthetic-junit-contract")
}

var _ = ReportAfterSuite("Write synthetic JUnit", func(report Report) {
	if err := internal.GenerateJUnitReport(report, junitReportPath); err != nil {
		Fail(fmt.Sprintf("failed to write synthetic JUnit: %v", err))
	}
})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:JUnitContract] Synthetic JUnit contract", Label("inherited-label"), func() {
	It("ordinary pass", Label("leaf-label"), func() {})

	It("ordinary failure", func() {
		if os.Getenv("JUNIT_FIXTURE_BLOCKING_FAILURE") == "true" {
			Expect(false).To(BeTrue(), "blocking boom")
		}
	})

	Context("informing context", Label(internal.InformingLabel), func() {
		It("informing pass", func() {})

		It("informing failure", func() {
			Expect(false).To(BeTrue(), "informing boom")
		})

		It("informing genuine skip", func() {
			Skip("platform not supported")
		})
	})
})
