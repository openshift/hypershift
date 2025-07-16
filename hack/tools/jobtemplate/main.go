package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Test struct {
	As           string                 `yaml:"as"`
	Steps        map[string]interface{} `yaml:"steps,omitempty"`
	Commands     string                 `yaml:"commands,omitempty"`
	Container    map[string]interface{} `yaml:"container,omitempty"`
	Capabilities []string               `yaml:"capabilities,omitempty"`
	Optional     bool                   `yaml:"optional,omitempty"`
	Raw          map[string]interface{} `yaml:",inline"`
}

type Config struct {
	Tests []Test               `yaml:"tests"`
	Raw   map[string]interface{} `yaml:",inline"`
}

var (
	configFile  string
	outputFile string
	testBlocks string
	buckets    int
)

func init() {
	flag.StringVar(&configFile, "config", "", "Path to the job config yaml file")
	flag.StringVar(&outputFile, "output", "", "Path to write the output yaml file (use '-' for stdout)")
	flag.StringVar(&testBlocks, "test-blocks", "", "Comma-separated list of test block names (as: values)")
	flag.IntVar(&buckets, "total-buckets", 0, "Total number of buckets to generate")
}

// detectIndentation determines the indentation used in the YAML file
func detectIndentation(content []byte) int {
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "  ") {
			// Find first line with indentation under a sequence item
			if strings.HasPrefix(strings.TrimSpace(line), "as:") ||
				strings.HasPrefix(strings.TrimSpace(line), "commands:") ||
				strings.HasPrefix(strings.TrimSpace(line), "container:") {
				return len(line) - len(strings.TrimLeft(line, " "))
			}
		}
	}
	return 2 // Default to 2 spaces if no indentation detected
}

// generateBucketedJobs applies bucketing to specific test blocks in a config
func generateBucketedJobs(content []byte, testBlockNames []string, totalBuckets int) ([]byte, error) {
	// First decode into a Node to preserve style
	var node yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(content))
	dec.KnownFields(false)
	if err := dec.Decode(&node); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %v", err)
	}

	indentSpaces := detectIndentation(content)

	// Find the tests sequence node
	var testsNode *yaml.Node
	if node.Kind == yaml.DocumentNode {
		node = *node.Content[0]
	}

	// Set the style for the root node and tests node to preserve indentation
	node.Style = yaml.Style(0)
	for i, n := range node.Content {
		if i%2 == 0 && n.Value == "tests" {
			testsNode = node.Content[i+1]
			testsNode.Style = yaml.Style(0)
			break
		}
	}
	if testsNode == nil {
		return nil, fmt.Errorf("no tests section found in yaml")
	}

	// Convert test blocks slice to a set for easy lookup
	testBlockSet := make(map[string]bool)
	for _, block := range testBlockNames {
		testBlockSet[strings.TrimSpace(block)] = true
	}

	// Create a new slice to hold all tests
	var modifiedTests []*yaml.Node

	// Process all tests
	for _, testNode := range testsNode.Content {
		// Find the "as" field to get the test name
		var testName string
		for i := 0; i < len(testNode.Content); i += 2 {
			if testNode.Content[i].Value == "as" {
				testName = testNode.Content[i+1].Value
				break
			}
		}

		if testBlockSet[testName] {
			// This test needs bucketing - create multiple copies
			for i := 1; i <= totalBuckets; i++ {
				// Deep copy the test by marshaling and unmarshaling
				testBytes, err := yaml.Marshal(testNode)
				if err != nil {
					return nil, fmt.Errorf("failed to marshal test: %v", err)
				}

				var bucketTest yaml.Node
				if err := yaml.Unmarshal(testBytes, &bucketTest); err != nil {
					return nil, fmt.Errorf("failed to unmarshal test: %v", err)
				}

				// The unmarshaled node will be a document node, get its content
				bucketTestNode := bucketTest.Content[0]

				// Update the test name with bucket suffix
				for j := 0; j < len(bucketTestNode.Content); j += 2 {
					if bucketTestNode.Content[j].Value == "as" {
						bucketTestNode.Content[j+1].Value = fmt.Sprintf("%s-b%d", testName, i)
						break
					}
				}

				// Set or update the env section
				var stepsNode *yaml.Node
				var envNode *yaml.Node
				for j := 0; j < len(bucketTestNode.Content); j += 2 {
					if bucketTestNode.Content[j].Value == "steps" {
						stepsNode = bucketTestNode.Content[j+1]
						// Find or create env section in steps
						for k := 0; k < len(stepsNode.Content); k += 2 {
							if stepsNode.Content[k].Value == "env" {
								envNode = stepsNode.Content[k+1]
								break
							}
						}
						break
					}
				}

				if stepsNode == nil {
					// Create steps section if it doesn't exist
					stepsNode = &yaml.Node{
						Kind:  yaml.MappingNode,
						Style: yaml.Style(indentSpaces),
					}
					bucketTestNode.Content = append(bucketTestNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "steps", Style: yaml.Style(indentSpaces)},
						stepsNode,
					)
				}

				if envNode == nil {
					// Create env section if it doesn't exist
					envNode = &yaml.Node{
						Kind:  yaml.MappingNode,
						Style: yaml.Style(indentSpaces),
					}
					stepsNode.Content = append(stepsNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "env", Style: yaml.Style(indentSpaces)},
						envNode,
					)
				}

				// Add or update BUCKET_NUM and BUCKET_TOTAL
				bucketNumFound := false
				bucketTotalFound := false
				for j := 0; j < len(envNode.Content); j += 2 {
					if envNode.Content[j].Value == "BUCKET_NUM" {
						envNode.Content[j+1].Value = fmt.Sprintf("%d", i)
						bucketNumFound = true
					} else if envNode.Content[j].Value == "BUCKET_TOTAL" {
						envNode.Content[j+1].Value = fmt.Sprintf("%d", totalBuckets)
						bucketTotalFound = true
					}
				}

				if !bucketNumFound {
					envNode.Content = append(envNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "BUCKET_NUM", Style: yaml.Style(indentSpaces)},
						&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", i), Style: yaml.Style(indentSpaces)},
					)
				}
				if !bucketTotalFound {
					envNode.Content = append(envNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "BUCKET_TOTAL", Style: yaml.Style(indentSpaces)},
						&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", totalBuckets), Style: yaml.Style(indentSpaces)},
					)
				}

				modifiedTests = append(modifiedTests, bucketTestNode)
			}
		} else {
			// This test doesn't need bucketing - keep it exactly as is
			modifiedTests = append(modifiedTests, testNode)
		}
	}

	// Replace tests content with modified version
	testsNode.Content = modifiedTests

	// Create a new encoder that preserves the input indentation
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indentSpaces)
	if err := enc.Encode(&node); err != nil {
		return nil, fmt.Errorf("failed to encode yaml: %v", err)
	}
	
	return buf.Bytes(), nil
}

func main() {
	flag.Parse()

	if configFile == "" || testBlocks == "" || buckets <= 0 {
		fmt.Fprintf(os.Stderr, "Required flags: -config, -test-blocks, and -total-buckets\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// If no output file specified, default to input file
	if outputFile == "" {
		outputFile = configFile
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read config file: %v\n", err)
		os.Exit(1)
	}

	blockNames := strings.Split(testBlocks, ",")
	output, err := generateBucketedJobs(content, blockNames, buckets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to generate bucketed jobs: %v\n", err)
		os.Exit(1)
	}

	// Write to stdout if outputFile is "-"
	if outputFile == "-" {
		_, err = os.Stdout.Write(output)
	} else {
		err = os.WriteFile(outputFile, output, 0644)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write output: %v\n", err)
		os.Exit(1)
	}
}
