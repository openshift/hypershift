//go:build e2ev2

package main

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTestBinary(t *testing.T) {
	binary := buildJUnitFixture(t)

	t.Run("When the synthetic suite has a blocking failure, it should fail and honor the JUnit contract", func(t *testing.T) {
		t.Setenv("JUNIT_FIXTURE_BLOCKING_FAILURE", "true")
		junitPath := filepath.Join(t.TempDir(), "junit.xml")
		err := runTestBinary(binary, "synthetic-cluster", "clusters", "", "", junitPath)
		if err == nil {
			t.Fatal("expected the synthetic suite to fail")
		}

		assertJUnitContract(t, parseJUnit(t, junitPath))
	})

	t.Run("When the synthetic suite has only informing failures, it should succeed", func(t *testing.T) {
		t.Setenv("JUNIT_FIXTURE_BLOCKING_FAILURE", "false")
		junitPath := filepath.Join(t.TempDir(), "junit.xml")
		if err := runTestBinary(binary, "synthetic-cluster", "clusters", "", "", junitPath); err != nil {
			t.Fatalf("expected informing failures not to fail the suite: %v", err)
		}

		report := parseJUnit(t, junitPath)
		informingFailure := findTestCase(t, report, "informing failure")
		if informingFailure.Failure == nil || !strings.Contains(informingFailure.Failure.Message, "informing boom") {
			t.Fatalf("expected informing failure in successful suite report, got %#v", informingFailure.Failure)
		}
	})

	t.Run("When a label filter selects one test, it should omit the other discovered tests", func(t *testing.T) {
		t.Setenv("JUNIT_FIXTURE_BLOCKING_FAILURE", "false")
		junitPath := filepath.Join(t.TempDir(), "junit.xml")
		if err := runTestBinary(binary, "synthetic-cluster", "clusters", "leaf-label", "", junitPath); err != nil {
			t.Fatalf("expected the selected test to pass: %v", err)
		}

		report := parseJUnit(t, junitPath)
		if len(report.Suites) != 1 || report.Suites[0].Tests != 1 || len(report.Suites[0].TestCases) != 1 {
			t.Fatalf("expected only one selected testcase, got %#v", report)
		}
		if !strings.Contains(report.Suites[0].TestCases[0].Name, "ordinary pass") {
			t.Fatalf("unexpected selected testcase: %#v", report.Suites[0].TestCases[0])
		}
		assertNoLifecycle(t, report.Suites[0].TestCases[0])
	})
}

func buildJUnitFixture(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "junit-fixture.test")
	cmd := exec.CommandContext(t.Context(), "go", "test", "-c", "-tags=e2ev2", "-o", binary, "./testdata/junitfixture")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=vendor")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build synthetic JUnit fixture: %v\n%s", err, output)
	}
	return binary
}

type junitTestSuites struct {
	Suites []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Skipped   int             `xml:"skipped,attr"`
	TestCases []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name       string          `xml:"name,attr"`
	Lifecycle  string          `xml:"lifecycle,attr"`
	Properties []junitProperty `xml:"properties>property"`
	Skipped    *junitMessage   `xml:"skipped"`
	Failure    *junitMessage   `xml:"failure"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitMessage struct {
	Message string `xml:"message,attr"`
}

func parseJUnit(t *testing.T, path string) junitTestSuites {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read JUnit report: %v", err)
	}
	var report junitTestSuites
	if err := xml.Unmarshal(data, &report); err != nil {
		t.Fatalf("parse JUnit report: %v\n%s", err, data)
	}
	return report
}

func assertJUnitContract(t *testing.T, report junitTestSuites) {
	t.Helper()
	if len(report.Suites) != 1 {
		t.Fatalf("expected one suite, got %d", len(report.Suites))
	}
	suite := report.Suites[0]
	if suite.Name != "synthetic-junit-contract" {
		t.Fatalf("suite name: got %q, want synthetic-junit-contract", suite.Name)
	}
	if suite.Tests != 5 || len(suite.TestCases) != 5 {
		t.Fatalf("expected five testcases, counts=%d cases=%d", suite.Tests, len(suite.TestCases))
	}
	if suite.Failures != 2 || suite.Skipped != 1 {
		t.Fatalf("unexpected suite counts: failures=%d skipped=%d", suite.Failures, suite.Skipped)
	}
	for _, tc := range suite.TestCases {
		if strings.HasPrefix(tc.Name, "[It] ") {
			t.Errorf("testcase name contains Ginkgo leaf node type: %q", tc.Name)
		}
		if strings.Contains(tc.Name, "inherited-label") || strings.Contains(tc.Name, "leaf-label") || strings.Contains(tc.Name, "Informing") {
			t.Errorf("testcase name contains a Ginkgo label: %q", tc.Name)
		}
		if !strings.Contains(tc.Name, "[sig-hypershift][Jira:Hypershift][Feature:JUnitContract]") {
			t.Errorf("testcase name dropped textual annotations: %q", tc.Name)
		}
	}

	ordinaryFailure := findTestCase(t, report, "ordinary failure")
	if ordinaryFailure.Failure == nil || !strings.Contains(ordinaryFailure.Failure.Message, "blocking boom") {
		t.Fatalf("expected ordinary failure, got %#v", ordinaryFailure.Failure)
	}
	assertNoLifecycle(t, ordinaryFailure)

	informingPass := findTestCase(t, report, "informing pass")
	assertLifecycle(t, informingPass)

	informingFailure := findTestCase(t, report, "informing failure")
	assertLifecycle(t, informingFailure)
	if informingFailure.Skipped != nil {
		t.Fatalf("informing failure rendered as skipped: %#v", informingFailure.Skipped)
	}
	if informingFailure.Failure == nil || !strings.Contains(informingFailure.Failure.Message, "informing boom") {
		t.Fatalf("expected informing failure, got %#v", informingFailure.Failure)
	}
	if strings.Contains(informingFailure.Failure.Message, "informing test failure") {
		t.Errorf("informing failure leaked its internal skip prefix: %q", informingFailure.Failure.Message)
	}

	informingSkip := findTestCase(t, report, "informing genuine skip")
	assertLifecycle(t, informingSkip)
	if informingSkip.Skipped == nil || !strings.Contains(informingSkip.Skipped.Message, "platform not supported") {
		t.Fatalf("expected genuine informing skip, got %#v", informingSkip.Skipped)
	}
}

func findTestCase(t *testing.T, report junitTestSuites, namePart string) junitTestCase {
	t.Helper()
	for _, suite := range report.Suites {
		for _, tc := range suite.TestCases {
			if strings.Contains(tc.Name, namePart) {
				return tc
			}
		}
	}
	t.Fatalf("testcase containing %q not found", namePart)
	return junitTestCase{}
}

func assertLifecycle(t *testing.T, tc junitTestCase) {
	t.Helper()
	if tc.Lifecycle != "informing" {
		t.Errorf("lifecycle attribute: got %q, want informing", tc.Lifecycle)
	}
	for _, property := range tc.Properties {
		if property.Name == "lifecycle" && property.Value == "informing" {
			return
		}
	}
	t.Errorf("missing informing lifecycle property: %#v", tc.Properties)
}

func assertNoLifecycle(t *testing.T, tc junitTestCase) {
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
