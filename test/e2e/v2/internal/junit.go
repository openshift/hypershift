//go:build e2ev2

package internal

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/ginkgo/v2/types"
)

const lifecycleInforming = "informing"

type junitProperties struct {
	XMLName    xml.Name        `xml:"properties"`
	Properties []junitProperty `xml:"property"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// GenerateJUnitReport writes the v2 E2E JUnit contract to dst. Ginkgo labels
// are omitted from testcase names, and specs excluded by the suite's label
// filter are omitted from the report. Specs labeled Informing receive lifecycle
// metadata, and assertion failures converted to skips by InformingAwareFailHandler
// are represented as informing failures without causing the Ginkgo suite to fail.
func GenerateJUnitReport(report types.Report, dst string) error {
	if dst == "" {
		return fmt.Errorf("JUnit report destination must not be empty")
	}

	// Work on a copy because ReportAfterSuite consumers share the report. Restore
	// informing assertion failures before asking Ginkgo to calculate JUnit state
	// and suite counts.
	report.SpecReports = slices.Clone(report.SpecReports)
	if report.SuiteConfig.LabelFilter != "" {
		matchesLabelFilter, err := types.ParseLabelFilter(report.SuiteConfig.LabelFilter)
		if err != nil {
			return fmt.Errorf("parse Ginkgo label filter: %w", err)
		}
		selected := make(types.SpecReports, 0, len(report.SpecReports))
		for _, spec := range report.SpecReports {
			if matchesLabelFilter(spec.Labels()) {
				selected = append(selected, spec)
			}
		}
		report.SpecReports = selected
	}

	informing := make([]bool, 0, len(report.SpecReports))
	for i := range report.SpecReports {
		spec := &report.SpecReports[i]
		if spec.LeafNodeType == types.NodeTypeIt {
			informing = append(informing, slices.Contains(spec.Labels(), InformingLabel))
		}
		if IsInformingFailureSkip(*spec) {
			spec.State = types.SpecStateFailed
			spec.Failure.Message = InformingFailureMessage(*spec)
		}
	}

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0770); err != nil {
		return fmt.Errorf("create JUnit report directory: %w", err)
	}

	raw, err := os.CreateTemp(dir, ".ginkgo-junit-*.xml")
	if err != nil {
		return fmt.Errorf("create temporary Ginkgo JUnit report: %w", err)
	}
	rawPath := raw.Name()
	if err := raw.Close(); err != nil {
		return fmt.Errorf("close temporary Ginkgo JUnit report: %w", err)
	}
	defer os.Remove(rawPath)

	if err := reporters.GenerateJUnitReportWithConfig(report, rawPath, reporters.JunitReportConfig{
		OmitLeafNodeType:    true,
		OmitSpecLabels:      true,
		OmitSuiteSetupNodes: true,
	}); err != nil {
		return fmt.Errorf("generate Ginkgo JUnit report: %w", err)
	}

	output, err := os.CreateTemp(dir, ".hypershift-junit-*.xml")
	if err != nil {
		return fmt.Errorf("create temporary HyperShift JUnit report: %w", err)
	}
	outputPath := output.Name()
	defer os.Remove(outputPath)

	if err := addLifecycleMetadata(rawPath, output, informing); err != nil {
		output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close temporary HyperShift JUnit report: %w", err)
	}
	if err := os.Chmod(outputPath, 0644); err != nil {
		return fmt.Errorf("set JUnit report permissions: %w", err)
	}
	if err := os.Rename(outputPath, dst); err != nil {
		return fmt.Errorf("install JUnit report: %w", err)
	}
	return nil
}

func addLifecycleMetadata(src string, dst io.Writer, informing []bool) error {
	input, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open generated Ginkgo JUnit report: %w", err)
	}
	defer input.Close()

	decoder := xml.NewDecoder(input)
	encoder := xml.NewEncoder(dst)
	encoder.Indent("  ", "    ")
	testcaseIndex := 0

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("decode generated Ginkgo JUnit report: %w", err)
		}

		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "testcase" {
			if testcaseIndex >= len(informing) {
				return fmt.Errorf("generated JUnit report has more testcases than Ginkgo spec reports")
			}
			isInforming := informing[testcaseIndex]
			testcaseIndex++
			if isInforming {
				start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: "lifecycle"}, Value: lifecycleInforming})
			}
			if err := encoder.EncodeToken(start); err != nil {
				return fmt.Errorf("encode JUnit testcase: %w", err)
			}
			if isInforming {
				if err := encoder.Encode(junitProperties{Properties: []junitProperty{{
					Name:  "lifecycle",
					Value: lifecycleInforming,
				}}}); err != nil {
					return fmt.Errorf("encode informing lifecycle property: %w", err)
				}
			}
			continue
		}

		if err := encoder.EncodeToken(token); err != nil {
			return fmt.Errorf("encode HyperShift JUnit report: %w", err)
		}
	}

	if testcaseIndex != len(informing) {
		return fmt.Errorf("generated JUnit report has %d testcases for %d Ginkgo spec reports", testcaseIndex, len(informing))
	}
	if err := encoder.Flush(); err != nil {
		return fmt.Errorf("flush HyperShift JUnit report: %w", err)
	}
	return nil
}
