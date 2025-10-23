package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

// JUnitTestSuites represents the root element
type JUnitTestSuites struct {
	XMLName   xml.Name         `xml:"testsuites"`
	Tests     int              `xml:"tests,attr"`
	Failures  int              `xml:"failures,attr"`
	Errors    int              `xml:"errors,attr"`
	Time      string           `xml:"time,attr"`
	TestSuite []JUnitTestSuite `xml:"testsuite"`
}

// JUnitTestSuite represents a test suite
type JUnitTestSuite struct {
	XMLName    xml.Name        `xml:"testsuite"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Skipped    int             `xml:"skipped,attr"`
	Time       string          `xml:"time,attr"`
	Name       string          `xml:"name,attr"`
	Timestamp  string          `xml:"timestamp,attr"`
	Properties *Properties     `xml:"properties,omitempty"`
	TestCases  []JUnitTestCase `xml:"testcase"`
}

// Properties represents the properties element
type Properties struct {
	XMLName  xml.Name   `xml:"properties"`
	Property []Property `xml:"property"`
}

// Property represents a single property
type Property struct {
	XMLName xml.Name `xml:"property"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}

// JUnitTestCase represents a test case
type JUnitTestCase struct {
	XMLName   xml.Name `xml:"testcase"`
	ClassName string   `xml:"classname,attr"`
	Name      string   `xml:"name,attr"`
	Time      string   `xml:"time,attr"`
	Failure   *Failure `xml:"failure,omitempty"`
}

// Failure represents a test failure
type Failure struct {
	XMLName xml.Name `xml:"failure"`
	Message string   `xml:"message,attr"`
	Type    string   `xml:"type,attr"`
	Content string   `xml:",chardata"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <junit-xml-file>\n", os.Args[0])
		os.Exit(1)
	}

	inputFile := os.Args[1]

	// Read the input file
	data, err := os.ReadFile(inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Parse the XML
	var testsuites JUnitTestSuites
	if err := xml.Unmarshal(data, &testsuites); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing XML: %v\n", err)
		os.Exit(1)
	}

	// Process each test suite
	for i := range testsuites.TestSuite {
		testsuites.TestSuite[i].TestCases = processTestCases(testsuites.TestSuite[i].TestCases)

		// Recalculate tests and failures counts for this test suite
		testsuites.TestSuite[i].Tests = len(testsuites.TestSuite[i].TestCases)
		testsuites.TestSuite[i].Failures = countFailures(testsuites.TestSuite[i].TestCases)
	}

	// Recalculate overall tests and failures counts for testsuites
	testsuites.Tests = 0
	testsuites.Failures = 0
	for _, suite := range testsuites.TestSuite {
		testsuites.Tests += suite.Tests
		testsuites.Failures += suite.Failures
	}

	// Generate output XML
	output, err := xml.MarshalIndent(testsuites, "", "    ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating XML: %v\n", err)
		os.Exit(1)
	}

	// Print with XML header
	fmt.Printf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n%s\n", output)
}

// countFailures counts the number of test cases with failures
func countFailures(testcases []JUnitTestCase) int {
	count := 0
	for _, tc := range testcases {
		if tc.Failure != nil {
			count++
		}
	}
	return count
}

// processTestCases processes test cases according to the rules
func processTestCases(testcases []JUnitTestCase) []JUnitTestCase {
	if len(testcases) == 0 {
		return testcases
	}

	// Build a map of test names for quick lookup
	testMap := make(map[string]*JUnitTestCase)
	for i := range testcases {
		testMap[testcases[i].Name] = &testcases[i]
	}

	// Identify tests to keep and tests to remove
	var result []JUnitTestCase
	toRemove := make(map[string]bool)

	for i := range testcases {
		testName := testcases[i].Name
		isParent := false

		// Check if this test is a parent of any other test
		for j := range testcases {
			if i == j {
				continue
			}
			otherName := testcases[j].Name

			// Check if testName is a prefix of otherName followed by a slash
			if strings.HasPrefix(otherName, testName+"/") {
				isParent = true
				break
			}
		}

		if isParent {
			toRemove[testName] = true
		}
	}

	// Build failure message map: for each child, collect all parent failures
	parentFailures := make(map[string][]string)

	for _, testcase := range testcases {
		if !toRemove[testcase.Name] {
			// This test is being kept, collect all parent failures
			var failures []string

			// Find all parents of this test
			parts := strings.Split(testcase.Name, "/")
			for i := 1; i <= len(parts); i++ {
				parentName := strings.Join(parts[:i], "/")
				if toRemove[parentName] {
					if parent, exists := testMap[parentName]; exists && parent.Failure != nil {
						failures = append(failures, parent.Failure.Content)
					}
				}
			}

			if len(failures) > 0 {
				parentFailures[testcase.Name] = failures
			}
		}
	}

	// Build the result, excluding removed tests and adding parent failures
	for _, testcase := range testcases {
		if !toRemove[testcase.Name] {
			tc := testcase

			// Add parent failures if any
			if failures, exists := parentFailures[testcase.Name]; exists {
				if tc.Failure != nil {
					// Prepend parent failures to existing failure
					tc.Failure.Content = strings.Join(failures, "\n") + "\n" + tc.Failure.Content
				} else {
					// Create a new failure with parent failures
					tc.Failure = &Failure{
						Message: "Failed",
						Type:    "",
						Content: strings.Join(failures, "\n"),
					}
				}
			}

			result = append(result, tc)
		}
	}

	return result
}
