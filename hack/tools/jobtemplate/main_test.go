package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestJobTemplate(t *testing.T) {
	// Create temp copy of input file
	inputData, err := os.ReadFile("testdata/input.yaml")
	if err != nil {
		t.Fatalf("Failed to read test input file: %v", err)
	}

	fmt.Printf("\n=== TestJobTemplate ===\n")
	fmt.Printf("Input YAML:\n%s\n", string(inputData))
	fmt.Printf("Generation args:\n- test blocks: [e2e-aws, e2e-aws-upgrade]\n- total buckets: 2\n")

	testBlocks := []string{"e2e-aws", "e2e-aws-upgrade"}
	output, err := generateBucketedJobs(inputData, testBlocks, 2)
	if err != nil {
		t.Fatalf("Failed to generate bucketed jobs: %v", err)
	}

	fmt.Printf("Output YAML:\n%s\n", string(output))

	var actualConfig Config
	if err := yaml.Unmarshal(output, &actualConfig); err != nil {
		t.Fatalf("Failed to parse output yaml: %v", err)
	}

	// Validate the output
	expectedTestNames := map[string]bool{
		"verify":             true,
		"e2e-aws-b1":         true,
		"e2e-aws-b2":         true,
		"e2e-aws-upgrade-b1": true,
		"e2e-aws-upgrade-b2": true,
	}

	for _, test := range actualConfig.Tests {
		if !expectedTestNames[test.As] {
			t.Errorf("Unexpected test block found: %s", test.As)
		}
		delete(expectedTestNames, test.As)

		if strings.HasPrefix(test.As, "e2e-aws") {
			// Validate env variables for bucketed tests
			env, ok := test.Steps["env"].(map[string]interface{})
			if !ok {
				t.Errorf("Test %s: env section not found or wrong type", test.As)
				continue
			}

			bucketNum, numOk := env["BUCKET_NUM"].(int)
			bucketTotal, totalOk := env["BUCKET_TOTAL"].(int)

			if !numOk || !totalOk {
				t.Errorf("Test %s: BUCKET_NUM or BUCKET_TOTAL not found or wrong type", test.As)
			}

			if bucketTotal != 2 {
				t.Errorf("Test %s: expected BUCKET_TOTAL=2, got %d", test.As, bucketTotal)
			}

			suffix := test.As[strings.LastIndex(test.As, "-b")+2:]
			expectedNum := int(suffix[0] - '0')
			if bucketNum != expectedNum {
				t.Errorf("Test %s: expected BUCKET_NUM=%d, got %d", test.As, expectedNum, bucketNum)
			}
		}
	}

	// Check that all expected test names were found
	for name := range expectedTestNames {
		t.Errorf("Expected test block not found: %s", name)
	}
}

// Helper function to run a test with different parameters and validate output
func runToolTest(t *testing.T, testBlocks []string, totalBuckets int, validateFunc func(config Config) error) {
	t.Helper()

	// Read input file
	inputData, err := os.ReadFile("testdata/input.yaml")
	if err != nil {
		t.Fatalf("Failed to read test input file: %v", err)
	}

	fmt.Printf("\nInput YAML:\n%s\n", string(inputData))
	fmt.Printf("Generation args:\n- test blocks: %v\n- total buckets: %d\n", testBlocks, totalBuckets)

	// Generate bucketed jobs
	output, err := generateBucketedJobs(inputData, testBlocks, totalBuckets)
	if err != nil {
		t.Fatalf("Failed to generate bucketed jobs: %v", err)
	}

	fmt.Printf("Output YAML:\n%s\n", string(output))

	var result Config
	if err := yaml.Unmarshal(output, &result); err != nil {
		t.Fatalf("Failed to parse output yaml: %v", err)
	}

	if err := validateFunc(result); err != nil {
		t.Error(err)
	}
}

// Test cases
func TestJobTemplateVariations(t *testing.T) {
	tests := []struct {
		name         string
		testBlocks   []string
		totalBuckets int
		validate     func(config Config) error
	}{
		{
			name:         "Single test block",
			testBlocks:   []string{"e2e-aws"},
			totalBuckets: 3,
			validate: func(config Config) error {
				// Create a map of expected test names - ONLY verify and e2e-aws variants
				expectedNames := map[string]bool{
					"verify":      true,
					"e2e-aws-b1": true,
					"e2e-aws-b2": true,
					"e2e-aws-b3": true,
					"e2e-aws-upgrade": true,
				}

				// Check each test in the config
				for _, test := range config.Tests {
					if !expectedNames[test.As] {
						return fmt.Errorf("unexpected test found: %s", test.As)
					}
					delete(expectedNames, test.As)

					// Validate bucket env vars for e2e-aws tests
					if strings.HasPrefix(test.As, "e2e-aws-b") {
						env, ok := test.Steps["env"].(map[string]interface{})
						if !ok {
							return fmt.Errorf("test %s: env section not found or wrong type", test.As)
						}

						bucketNum, numOk := env["BUCKET_NUM"].(int)
						bucketTotal, totalOk := env["BUCKET_TOTAL"].(int)

						if !numOk || !totalOk {
							return fmt.Errorf("test %s: BUCKET_NUM or BUCKET_TOTAL not found or wrong type", test.As)
						}

						if bucketTotal != 3 {
							return fmt.Errorf("test %s: expected BUCKET_TOTAL=3, got %d", test.As, bucketTotal)
						}

						suffix := test.As[strings.LastIndex(test.As, "-b")+2:]
						expectedNum := int(suffix[0] - '0')
						if bucketNum != expectedNum {
							return fmt.Errorf("test %s: expected BUCKET_NUM=%d, got %d", test.As, expectedNum, bucketNum)
						}
					}
				}

				// Any remaining expected names weren't found
				if len(expectedNames) > 0 {
					var missing []string
					for name := range expectedNames {
						missing = append(missing, name)
					}
					return fmt.Errorf("expected tests not found: %v", missing)
				}

				return nil
			},
		},
		{
			name:         "No matching blocks",
			testBlocks:   []string{"non-existent-test"},
			totalBuckets: 2,
			validate: func(config Config) error {
				if len(config.Tests) != 3 { // original tests unchanged
					return fmt.Errorf("expected 3 tests, got %d", len(config.Tests))
				}
				return nil
			},
		},
		{
			name:         "Multiple test blocks",
			testBlocks:   []string{"e2e-aws", "e2e-aws-upgrade"},
			totalBuckets: 2,
			validate: func(config Config) error {
				expectedNames := []string{
					"verify",
					"e2e-aws-b1", "e2e-aws-b2",
					"e2e-aws-upgrade-b1", "e2e-aws-upgrade-b2",
				}
				return validateTestNames(config, expectedNames)
			},
		},
		{
			name:         "Empty test blocks",
			testBlocks:   []string{},
			totalBuckets: 2,
			validate: func(config Config) error {
				if len(config.Tests) != 3 { // original tests unchanged
					return fmt.Errorf("expected tests to be unchanged when no test blocks specified")
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fmt.Printf("\n=== TestJobTemplateVariations/%s ===\n", tc.name)
			runToolTest(t, tc.testBlocks, tc.totalBuckets, tc.validate)
		})
	}
}

func validateTestNames(config Config, expectedNames []string) error {
	actualNames := make(map[string]bool)
	for _, test := range config.Tests {
		actualNames[test.As] = true
	}

	for _, name := range expectedNames {
		if !actualNames[name] {
			return fmt.Errorf("expected test %q not found", name)
		}
		delete(actualNames, name)
	}

	if len(actualNames) > 0 {
		var unexpected []string
		for name := range actualNames {
			unexpected = append(unexpected, name)
		}
		return fmt.Errorf("unexpected tests found: %v", unexpected)
	}

	return nil
}
