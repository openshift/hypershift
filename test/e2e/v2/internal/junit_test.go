//go:build e2ev2

package internal

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2/types"
)

func TestBuildLifecycleReport(t *testing.T) {
	tests := []struct {
		name  string
		specs types.SpecReports

		wantTests  int
		wantFailed int

		// Per-testcase assertions, indexed by position in the output.
		// Empty means only suite-level counts are checked.
		wantCases []wantCase
	}{
		{
			name: "When an informing failure was converted to a skip, it should emit a failure",
			specs: types.SpecReports{
				informingFailureSkip("should have custom labels", "expected labels to match"),
			},
			wantTests:  1,
			wantFailed: 1,
			wantCases: []wantCase{
				{
					lifecycle:      lifecycleInforming,
					failureMessage: "expected labels to match",
				},
			},
		},
		{
			name: "When an informing test passes, it should be excluded",
			specs: types.SpecReports{
				informingPass("should have custom tolerations"),
			},
			wantTests: 0,
		},
		{
			name: "When specs are non-informing, it should exclude them entirely",
			specs: types.SpecReports{
				{
					LeafNodeType:             types.NodeTypeIt,
					ContainerHierarchyTexts:  []string{"Suite"},
					ContainerHierarchyLabels: [][]string{{}},
					LeafNodeText:             "blocking pass",
					State:                    types.SpecStatePassed,
					RunTime:                  time.Second,
				},
				{
					LeafNodeType:             types.NodeTypeIt,
					LeafNodeLabels:           []string{"blocking"},
					ContainerHierarchyTexts:  []string{"Suite"},
					ContainerHierarchyLabels: [][]string{{}},
					LeafNodeText:             "blocking fail",
					State:                    types.SpecStateFailed,
					RunTime:                  time.Second,
					Failure:                  types.Failure{Message: "boom"},
				},
			},
			wantTests: 0,
		},
		{
			name: "When an informing test has a regular skip, it should be excluded",
			specs: types.SpecReports{
				{
					LeafNodeType:             types.NodeTypeIt,
					LeafNodeLabels:           []string{InformingLabel},
					ContainerHierarchyTexts:  []string{"Suite"},
					ContainerHierarchyLabels: [][]string{{}},
					LeafNodeText:             "should skip",
					State:                    types.SpecStateSkipped,
					Failure:                  types.Failure{Message: "platform not supported"},
				},
			},
			wantTests: 0,
		},
		{
			name: "When a spec is a BeforeSuite node, it should exclude it",
			specs: types.SpecReports{
				{
					LeafNodeType:             types.NodeTypeBeforeSuite,
					LeafNodeLabels:           []string{InformingLabel},
					ContainerHierarchyTexts:  []string{},
					ContainerHierarchyLabels: [][]string{{}},
					LeafNodeText:             "setup",
					State:                    types.SpecStatePassed,
				},
			},
			wantTests: 0,
		},
		{
			name: "When an informing test panics directly, it should be excluded",
			specs: types.SpecReports{
				{
					LeafNodeType:             types.NodeTypeIt,
					LeafNodeLabels:           []string{InformingLabel},
					ContainerHierarchyTexts:  []string{"Suite"},
					ContainerHierarchyLabels: [][]string{{}},
					LeafNodeText:             "should not panic",
					State:                    types.SpecStatePanicked,
					RunTime:                  500 * time.Millisecond,
					Failure: types.Failure{
						Message:  "runtime error: nil pointer",
						Location: types.CodeLocation{FileName: "x_test.go", LineNumber: 7},
					},
				},
			},
			wantTests: 0,
		},
		{
			name: "When specs are mixed, it should include only informing failure skips",
			specs: types.SpecReports{
				informingPass("informing pass"),
				{
					LeafNodeType:             types.NodeTypeIt,
					ContainerHierarchyTexts:  []string{"Suite"},
					ContainerHierarchyLabels: [][]string{{}},
					LeafNodeText:             "blocking pass",
					State:                    types.SpecStatePassed,
					RunTime:                  time.Second,
				},
				informingFailureSkip("informing fail", "expected X"),
				{
					LeafNodeType:             types.NodeTypeIt,
					ContainerHierarchyTexts:  []string{"Suite"},
					ContainerHierarchyLabels: [][]string{{}},
					LeafNodeText:             "blocking fail",
					State:                    types.SpecStateFailed,
					RunTime:                  time.Second,
					Failure:                  types.Failure{Message: "bad"},
				},
			},
			wantTests:  1,
			wantFailed: 1,
			wantCases: []wantCase{
				{lifecycle: lifecycleInforming, failureMessage: "expected X"},
			},
		},
		{
			name:      "When the spec list is empty, it should produce an empty suite",
			specs:     types.SpecReports{},
			wantTests: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildInformingTestsLifecycleReport("test-suite", tt.specs)

			if n := len(result.Suites); n != 1 {
				t.Fatalf("expected 1 suite, got %d", n)
			}
			suite := result.Suites[0]

			if suite.NumTests != tt.wantTests {
				t.Errorf("NumTests: got %d, want %d", suite.NumTests, tt.wantTests)
			}
			if suite.NumFailed != tt.wantFailed {
				t.Errorf("NumFailed: got %d, want %d", suite.NumFailed, tt.wantFailed)
			}
			if n := len(suite.TestCases); n != tt.wantTests {
				t.Fatalf("len(TestCases): got %d, want %d", n, tt.wantTests)
			}

			for i, want := range tt.wantCases {
				tc := suite.TestCases[i]
				if tc.Lifecycle != want.lifecycle {
					t.Errorf("case[%d] lifecycle: got %q, want %q", i, tc.Lifecycle, want.lifecycle)
				}
				if tc.FailureOutput == nil {
					t.Fatalf("case[%d] expected failure output", i)
				}
				if want.failureMessage != "" && tc.FailureOutput.Message != want.failureMessage {
					t.Errorf("case[%d] failure message: got %q, want %q", i, tc.FailureOutput.Message, want.failureMessage)
				}
				assertLifecycleProperty(t, i, tc, want.lifecycle)
			}
		})
	}
}

func TestBuildLifecycleReport_XMLRoundTrip(t *testing.T) {
	specs := types.SpecReports{
		informingFailureSkip("should emit lifecycle", "expected value"),
	}

	result := BuildInformingTestsLifecycleReport("e2e", specs)
	data, err := xml.MarshalIndent(result, "", "    ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	xmlStr := string(data)

	for _, want := range []string{
		`lifecycle="informing"`,
		`<property name="lifecycle" value="informing"`,
		`<failure`,
	} {
		if !strings.Contains(xmlStr, want) {
			t.Errorf("XML missing %q:\n%s", want, xmlStr)
		}
	}
	if strings.Contains(xmlStr, `<skipped`) {
		t.Errorf("XML should not contain <skipped> for informing failure:\n%s", xmlStr)
	}

	var parsed JUnitTestSuites
	if err := xml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if n := len(parsed.Suites); n != 1 {
		t.Fatalf("round-trip produced %d suites, want 1", n)
	}
	if n := len(parsed.Suites[0].TestCases); n != 1 {
		t.Fatalf("round-trip produced %d cases, want 1", n)
	}
	tc := parsed.Suites[0].TestCases[0]
	if tc.Lifecycle != lifecycleInforming {
		t.Errorf("round-trip lifecycle: got %q, want %q", tc.Lifecycle, lifecycleInforming)
	}
}

// --- helpers ---

type wantCase struct {
	lifecycle      string
	failureMessage string
}

func informingFailureSkip(leafText, originalMessage string) types.SpecReport {
	return types.SpecReport{
		LeafNodeType:             types.NodeTypeIt,
		LeafNodeLabels:           []string{InformingLabel},
		ContainerHierarchyTexts:  []string{"[sig-hypershift] Suite"},
		ContainerHierarchyLabels: [][]string{{}},
		LeafNodeText:             leafText,
		State:                    types.SpecStateSkipped,
		RunTime:                  2 * time.Second,
		Failure: types.Failure{
			Message:  informingSkipPrefix + originalMessage,
			Location: types.CodeLocation{FileName: "test.go", LineNumber: 42},
		},
	}
}

func informingPass(leafText string) types.SpecReport {
	return types.SpecReport{
		LeafNodeType:             types.NodeTypeIt,
		LeafNodeLabels:           []string{InformingLabel},
		ContainerHierarchyTexts:  []string{"[sig-hypershift] Suite"},
		ContainerHierarchyLabels: [][]string{{}},
		LeafNodeText:             leafText,
		State:                    types.SpecStatePassed,
		RunTime:                  time.Second,
	}
}

func assertLifecycleProperty(t *testing.T, idx int, tc *JUnitTestCase, expected string) {
	t.Helper()
	for _, p := range tc.Properties {
		if p.Name == "lifecycle" && p.Value == expected {
			return
		}
	}
	t.Errorf("case[%d]: expected lifecycle property %q, not found", idx, expected)
}
