//go:build e2ev2

package internal

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2/types"
)

func TestGenerateJUnitReport(t *testing.T) {
	tests := []struct {
		name   string
		spec   types.SpecReport
		assert func(*testing.T, parsedTestCase)
	}{
		{
			name: "When a regular test passes with inherited and leaf labels, it should omit labels from its name",
			spec: specReport("regular pass", types.SpecStatePassed, []string{"leaf-label"}, [][]string{{"container-label"}}),
			assert: func(t *testing.T, tc parsedTestCase) {
				if strings.HasPrefix(tc.Name, "[It] ") {
					t.Fatalf("testcase name contains Ginkgo leaf node type: %q", tc.Name)
				}
				if strings.Contains(tc.Name, "leaf-label") || strings.Contains(tc.Name, "container-label") {
					t.Fatalf("testcase name contains Ginkgo labels: %q", tc.Name)
				}
				if !strings.Contains(tc.Name, "[sig-hypershift][Jira:Hypershift][Feature:JUnitContract]") {
					t.Fatalf("testcase name dropped textual annotations: %q", tc.Name)
				}
				assertNoLifecycle(t, tc)
			},
		},
		{
			name: "When a regular test fails, it should emit a blocking failure",
			spec: func() types.SpecReport {
				spec := specReport("regular failure", types.SpecStateFailed, nil, [][]string{{}})
				spec.Failure = types.Failure{Message: "blocking boom"}
				return spec
			}(),
			assert: func(t *testing.T, tc parsedTestCase) {
				if tc.Failure == nil || tc.Failure.Message != "blocking boom" {
					t.Fatalf("expected blocking failure, got %#v", tc.Failure)
				}
				assertNoLifecycle(t, tc)
			},
		},
		{
			name:   "When an informing test passes, it should emit informing lifecycle metadata",
			spec:   specReport("informing pass", types.SpecStatePassed, []string{InformingLabel}, [][]string{{}}),
			assert: assertInformingLifecycle,
		},
		{
			name: "When an informing assertion failure was converted to a skip, it should emit an informing failure",
			spec: informingFailureSkip("informing failure", "informing boom"),
			assert: func(t *testing.T, tc parsedTestCase) {
				assertInformingLifecycle(t, tc)
				if tc.Skipped != nil {
					t.Fatalf("informing assertion failure was rendered as skipped: %#v", tc.Skipped)
				}
				if tc.Failure == nil || tc.Failure.Message != "informing boom" {
					t.Fatalf("expected restored informing failure, got %#v", tc.Failure)
				}
				if strings.Contains(tc.Failure.Message, informingSkipPrefix) {
					t.Fatalf("failure contains internal informing prefix: %q", tc.Failure.Message)
				}
			},
		},
		{
			name: "When an informing test is genuinely skipped, it should remain skipped with lifecycle metadata",
			spec: func() types.SpecReport {
				spec := specReport("informing skip", types.SpecStateSkipped, []string{InformingLabel}, [][]string{{}})
				spec.Failure = types.Failure{Message: "platform not supported"}
				return spec
			}(),
			assert: func(t *testing.T, tc parsedTestCase) {
				assertInformingLifecycle(t, tc)
				if tc.Skipped == nil || !strings.Contains(tc.Skipped.Message, "platform not supported") {
					t.Fatalf("expected genuine skip, got %#v", tc.Skipped)
				}
				if tc.Failure != nil {
					t.Fatalf("genuine skip was rendered as a failure: %#v", tc.Failure)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := types.Report{
				SuiteDescription: "JUnit contract",
				SuitePath:        "/hypershift/test/e2e/v2",
				SuiteSucceeded:   true,
				StartTime:        time.Date(2026, time.September, 10, 8, 12, 16, 0, time.UTC),
				EndTime:          time.Date(2026, time.September, 10, 8, 12, 18, 0, time.UTC),
				RunTime:          2 * time.Second,
				SpecReports:      types.SpecReports{tt.spec},
			}
			path := filepath.Join(t.TempDir(), "junit.xml")
			if err := GenerateJUnitReport(report, path); err != nil {
				t.Fatalf("GenerateJUnitReport returned an error: %v", err)
			}

			parsed := parseJUnitReport(t, path)
			if len(parsed.Suites) != 1 || len(parsed.Suites[0].TestCases) != 1 {
				t.Fatalf("expected one suite with one testcase, got %#v", parsed)
			}
			if parsed.Suites[0].Name != report.SuiteDescription {
				t.Fatalf("suite name: got %q, want %q", parsed.Suites[0].Name, report.SuiteDescription)
			}
			tt.assert(t, parsed.Suites[0].TestCases[0])
		})
	}

	t.Run("When the destination is empty, it should return an error", func(t *testing.T) {
		if err := GenerateJUnitReport(types.Report{}, ""); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("When the report contains suite setup nodes, it should omit them", func(t *testing.T) {
		report := types.Report{
			SuiteDescription: "JUnit contract",
			SpecReports: types.SpecReports{
				{LeafNodeType: types.NodeTypeBeforeSuite, LeafNodeText: "setup", State: types.SpecStatePassed},
				specReport("test", types.SpecStatePassed, nil, [][]string{{"container-label"}}),
			},
		}
		path := filepath.Join(t.TempDir(), "junit.xml")
		if err := GenerateJUnitReport(report, path); err != nil {
			t.Fatalf("GenerateJUnitReport returned an error: %v", err)
		}
		parsed := parseJUnitReport(t, path)
		if len(parsed.Suites) != 1 || len(parsed.Suites[0].TestCases) != 1 {
			t.Fatalf("expected only the It testcase, got %#v", parsed)
		}
	})

	t.Run("When specs do not match the suite label filter, it should omit them", func(t *testing.T) {
		report := types.Report{
			SuiteDescription: "JUnit contract",
			SuiteConfig:      types.SuiteConfig{LabelFilter: "selected"},
			SpecReports: types.SpecReports{
				specReport("selected pass", types.SpecStatePassed, []string{"selected"}, [][]string{{}}),
				specReport("filtered informing skip", types.SpecStateSkipped, []string{InformingLabel, "other-group"}, [][]string{{}}),
			},
		}
		path := filepath.Join(t.TempDir(), "junit.xml")
		if err := GenerateJUnitReport(report, path); err != nil {
			t.Fatalf("GenerateJUnitReport returned an error: %v", err)
		}
		parsed := parseJUnitReport(t, path)
		if len(parsed.Suites) != 1 || len(parsed.Suites[0].TestCases) != 1 {
			t.Fatalf("expected only the selected testcase, got %#v", parsed)
		}
		if parsed.Suites[0].Tests != 1 || parsed.Suites[0].Skipped != 0 {
			t.Fatalf("unexpected suite counts: tests=%d skipped=%d", parsed.Suites[0].Tests, parsed.Suites[0].Skipped)
		}
		if !strings.Contains(parsed.Suites[0].TestCases[0].Name, "selected pass") {
			t.Fatalf("unexpected selected testcase: %#v", parsed.Suites[0].TestCases[0])
		}
	})
}

func TestAddLifecycleMetadata(t *testing.T) {
	t.Run("When testcase and spec counts differ, it should return an error", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "ginkgo.xml")
		if err := os.WriteFile(src, []byte(`<testsuites><testsuite><testcase name="one"/></testsuite></testsuites>`), 0600); err != nil {
			t.Fatalf("write source JUnit report: %v", err)
		}
		var output strings.Builder
		if err := addLifecycleMetadata(src, &output, nil); err == nil {
			t.Fatal("expected an error")
		}
	})
}

type parsedTestSuites struct {
	Suites []parsedTestSuite `xml:"testsuite"`
}

type parsedTestSuite struct {
	Name      string           `xml:"name,attr"`
	Tests     int              `xml:"tests,attr"`
	Failures  int              `xml:"failures,attr"`
	Skipped   int              `xml:"skipped,attr"`
	TestCases []parsedTestCase `xml:"testcase"`
}

type parsedTestCase struct {
	Name       string          `xml:"name,attr"`
	Lifecycle  string          `xml:"lifecycle,attr"`
	Properties []junitProperty `xml:"properties>property"`
	Skipped    *parsedMessage  `xml:"skipped"`
	Failure    *parsedMessage  `xml:"failure"`
}

type parsedMessage struct {
	Message string `xml:"message,attr"`
}

func parseJUnitReport(t *testing.T, path string) parsedTestSuites {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read JUnit report: %v", err)
	}
	var report parsedTestSuites
	if err := xml.Unmarshal(data, &report); err != nil {
		t.Fatalf("parse JUnit report: %v\n%s", err, data)
	}
	return report
}

func specReport(leafText string, state types.SpecState, leafLabels []string, containerLabels [][]string) types.SpecReport {
	return types.SpecReport{
		LeafNodeType:             types.NodeTypeIt,
		LeafNodeText:             leafText,
		LeafNodeLabels:           leafLabels,
		ContainerHierarchyTexts:  []string{"[sig-hypershift][Jira:Hypershift][Feature:JUnitContract] Synthetic suite"},
		ContainerHierarchyLabels: containerLabels,
		State:                    state,
		StartTime:                time.Date(2026, time.September, 10, 8, 12, 16, 0, time.UTC),
		EndTime:                  time.Date(2026, time.September, 10, 8, 12, 17, 0, time.UTC),
		RunTime:                  time.Second,
	}
}

func informingFailureSkip(leafText, originalMessage string) types.SpecReport {
	spec := specReport(leafText, types.SpecStateSkipped, []string{InformingLabel}, [][]string{{}})
	spec.Failure = types.Failure{
		Message:  informingSkipPrefix + originalMessage,
		Location: types.CodeLocation{FileName: "synthetic_test.go", LineNumber: 42},
	}
	return spec
}

func assertInformingLifecycle(t *testing.T, tc parsedTestCase) {
	t.Helper()
	if tc.Lifecycle != lifecycleInforming {
		t.Errorf("lifecycle attribute: got %q, want %q", tc.Lifecycle, lifecycleInforming)
	}
	for _, property := range tc.Properties {
		if property.Name == "lifecycle" && property.Value == lifecycleInforming {
			return
		}
	}
	t.Errorf("expected lifecycle property %q, got %#v", lifecycleInforming, tc.Properties)
}

func assertNoLifecycle(t *testing.T, tc parsedTestCase) {
	t.Helper()
	if tc.Lifecycle != "" {
		t.Errorf("unexpected lifecycle attribute %q", tc.Lifecycle)
	}
	for _, property := range tc.Properties {
		if property.Name == "lifecycle" {
			t.Errorf("unexpected lifecycle property %#v", property)
		}
	}
}
