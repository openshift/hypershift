package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

var (
	ROSAEnvironments = map[string][]string{
		"int": {
			"osd-fleet-manager-integration",
		},
		"stage": {
			"osd-fleet-manager-stage",
		},
		"prod-canary": {
			"osd-fleet-manager-production-canary",
		},
		"prod-group1": {
			"osd-fleet-manager-production-af-south-1",
			"osd-fleet-manager-production-ap-southeast-3",
			"osd-fleet-manager-production-ap-southeast-4",
			"osd-fleet-manager-production-ap-south-1",
			"osd-fleet-manager-production-ap-south-2",
		},
		"prod-group2": {
			"osd-fleet-manager-production-ap-northeast-1",
			"osd-fleet-manager-production-ap-northeast-2",
			"osd-fleet-manager-production-ap-northeast-3",
			"osd-fleet-manager-production-ap-southeast-1",
			"osd-fleet-manager-production-ap-southeast-2",
			"osd-fleet-manager-production-eu-north-1",
			"osd-fleet-manager-production-adobe-test",
		},
		"prod-group3": {
			"osd-fleet-manager-production-eu-west-2",
			"osd-fleet-manager-production-eu-central-1",
			"osd-fleet-manager-production-ca-central-1",
			"osd-fleet-manager-production-eu-south-1",
			"osd-fleet-manager-production-us-east-1",
		},
		"prod-group4": {
			"osd-fleet-manager-production-eu-west-1",
			"osd-fleet-manager-production-us-west-2",
			"osd-fleet-manager-production-us-east-2",
			"osd-fleet-manager-production-me-south-1",
			"osd-fleet-manager-production-sa-east-1",
			"osd-fleet-manager-production-ap-east-1",
		},
		"prod-group5": {
			"osd-fleet-manager-production-eu-central-2",
			"osd-fleet-manager-production-eu-south-2",
			"osd-fleet-manager-production-eu-west-3",
			"osd-fleet-manager-production-me-central-1",
		},
	}

	file      = flag.String("file", "", "The path to the deployment specification file.")
	env       = flag.String("env", "", "The environment to change in the deployment spec.")
	oldTag    = flag.String("old-tag", "", "The old tag used in search.")
	newTag    = flag.String("new-tag", "", "The new tag to use.")
	newCommit = flag.String("new-commit", "", "The new commit SHA to use.")
)

func main() {
	// Check all flags were used
	flag.Parse()
	if file != nil && len(*file) == 0 ||
		env != nil && len(*env) == 0 ||
		oldTag != nil && len(*oldTag) == 0 ||
		newTag != nil && len(*newTag) == 0 ||
		newCommit != nil && len(*newCommit) == 0 {
		log.Fatalf("all flags are required")
	}

	// Verify the right env value was used
	if !isValidEnvironment(*env) {
		log.Fatalf("invalid environment: %s", *env)
	}
	log.Print("Updating deployment spec for environment: ", *env)

	// Read the deployment spec
	deploymentFileContents, err := os.ReadFile(*file)
	if err != nil {
		log.Fatal("failed to read deployment file: ", err)
	}

	// Split the deployment spec content into a slice of strings
	lines := strings.Split(string(deploymentFileContents), "\n")

	// Update the deployment spec based on the environment
	log.Printf("Updating tag from %s to %s and updating SHA to %s", *oldTag, *newTag, *newCommit)
	err = updateDeploymentByEnvironment(*env, lines, *oldTag, *newTag, *newCommit)
	if err != nil {
		log.Fatalf("failed to update deployment: %v", err)
	}

	// Join the deployment spec back into one file
	output := strings.Join(lines, "\n")

	// Write the modified contents back to the file
	err = os.WriteFile(*file, []byte(output), 0644)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

}

// updateDeploymentByEnvironment updates the sectors in a particular ROSA environment
func updateDeploymentByEnvironment(env string, lines []string, oldTag, newTag, newCommit string) error {
	for _, sector := range ROSAEnvironments[env] {
		err := updateTagAndCommitSHA(&lines, sector, oldTag, newTag, newCommit)
		if err != nil {
			return err
		}
	}

	return nil
}

// isValidEnvironment verifies the environment passed through the flag is valid
func isValidEnvironment(env string) bool {
	_, valid := ROSAEnvironments[env]
	return valid
}

// updateTagAndCommitSHA replaces the git tag in one line and the commit sha in the following line in the deployment file
//
// Example lines containing the git tag and the commit sha
// # openshift/hypershift.git git-branch: main, git-tag: v0.1.32, git-commit: image tag
// hypershift-operator: quay.io/acm-d/rhtap-hypershift-operator:9e84188
func updateTagAndCommitSHA(lines *[]string, sector, oldTag, newTag, newCommit string) error {
	found := false
	modified := false
	fileContent := *lines

	for i, line := range fileContent {
		// Once the sector is found, keep reading each line until we hit the line with the git tag
		if found {
			if strings.Contains(line, oldTag) {
				// Replace the tag in the line
				fileContent[i] = strings.Replace(line, oldTag, newTag, 1)

				// The next line should be the commit; update the commit
				commitStrings := strings.Split(fileContent[i+1], ":")
				if len(commitStrings) != 3 {
					return fmt.Errorf("invalid commit format")
				}

				commitStrings[2] = newCommit
				fileContent[i+1] = strings.Join(commitStrings, ":")
				modified = true
				log.Printf("Updated sector %s", sector)

				break
			}

			// We're starting into another sector, so we need to break
			if strings.Contains(line, "  - name: ") {
				break
			}
		}

		// We found the sector we want to update
		if strings.Contains(line, sector) {
			found = true
		}
	}

	if !found {
		log.Printf("could not find the sector in the file: %s", sector)
	}

	if found && !modified {
		log.Printf("did not find a line containing the old tag, %s, for sector '%s'", oldTag, sector)
	}

	return nil
}
