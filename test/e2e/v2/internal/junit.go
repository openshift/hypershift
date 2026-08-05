//go:build e2ev2

package internal

import (
	"encoding/xml"

	"github.com/onsi/ginkgo/v2/types"
)

const lifecycleInforming = "informing"

type JUnitTestSuites struct {
	XMLName xml.Name          `xml:"testsuites"`
	Suites  []*JUnitTestSuite `xml:"testsuite"`
}

type JUnitTestSuite struct {
	XMLName    xml.Name         `xml:"testsuite"`
	Name       string           `xml:"name,attr"`
	NumTests   int              `xml:"tests,attr"`
	NumSkipped int              `xml:"skipped,attr"`
	NumFailed  int              `xml:"failures,attr"`
	Duration   float64          `xml:"time,attr"`
	TestCases  []*JUnitTestCase `xml:"testcase"`
}

type JUnitTestCase struct {
	XMLName       xml.Name            `xml:"testcase"`
	Name          string              `xml:"name,attr"`
	Duration      float64             `xml:"time,attr"`
	Lifecycle     string              `xml:"lifecycle,attr,omitempty"`
	Properties    []*JUnitProperty    `xml:"properties>property,omitempty"`
	SkipMessage   *JUnitSkipMessage   `xml:"skipped,omitempty"`
	FailureOutput *JUnitFailureOutput `xml:"failure,omitempty"`
}

type JUnitProperty struct {
	XMLName xml.Name `xml:"property"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}

type JUnitSkipMessage struct {
	XMLName xml.Name `xml:"skipped"`
	Message string   `xml:"message,attr,omitempty"`
}

type JUnitFailureOutput struct {
	XMLName xml.Name `xml:"failure"`
	Message string   `xml:"message,attr,omitempty"`
	Output  string   `xml:",chardata"`
}

// BuildInformingTestsLifecycleReport builds a JUnit test suite containing only informing test
// failures from the Ginkgo report. Informing failures converted to skips by
// InformingAwareFailHandler are re-emitted as failures with lifecycle="informing".
// ci-to-bigquery reads this attribute and populates the lifecycle column in
// BigQuery, making informing failures visible to Component Readiness.
func BuildInformingTestsLifecycleReport(suiteName string, specReports types.SpecReports) *JUnitTestSuites {
	suite := &JUnitTestSuite{
		Name: suiteName + " [informing]",
	}

	for _, spec := range specReports {
		if !IsInformingFailureSkip(spec) {
			continue
		}

		msg := InformingFailureMessage(spec)
		tc := &JUnitTestCase{
			Name:      spec.FullText(),
			Duration:  spec.RunTime.Seconds(),
			Lifecycle: lifecycleInforming,
			Properties: []*JUnitProperty{
				{Name: "lifecycle", Value: lifecycleInforming},
			},
			FailureOutput: &JUnitFailureOutput{
				Message: msg,
				Output:  spec.Failure.Location.String(),
			},
		}

		suite.TestCases = append(suite.TestCases, tc)
		suite.NumTests++
		suite.NumFailed++
	}

	suite.Duration = sumDuration(suite.TestCases)

	return &JUnitTestSuites{
		Suites: []*JUnitTestSuite{suite},
	}
}

func sumDuration(cases []*JUnitTestCase) float64 {
	var total float64
	for _, tc := range cases {
		total += tc.Duration
	}
	return total
}
