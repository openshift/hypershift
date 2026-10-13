package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"k8s.io/utils/ptr"
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
			"osd-fleet-manager-production-ap-southeast-5",
		},
		// These envs belongs to Fedramp and HCM are not managing the upgrades
		// xref: https://redhat-internal.slack.com/archives/C081W589GRG/p1750926263072849?thread_ts=1750681329.961589&cid=C081W589GRG
		"no-update": {
			"osd-fleet-manager-production-ca-west-1",
			"osd-fleet-manager-production-il-central-1",
		},
	}

	autoCommitChanges = false
	branchName        = ""

	file       = flag.String("file", "", "The path to the deployment specification file.")
	env        = flag.String("env", "", "The environment to change in the deployment spec.")
	oldTag     = flag.String("old-tag", "", "The old tag used in search.")
	newTag     = flag.String("new-tag", "", "The new tag to use.")
	newCommit  = flag.String("new-commit", "", "The new commit SHA to use.")
	jiraTicket = flag.String("jira-ticket", "", "Uses the jira ticket to create a branch with the same name with the environment added on.")
	token      = flag.String("token", "", "The token to use to authenticate to Gitlab API.")
	remote     = flag.String("remote", "origin", "The remote branch to use when committing changes.")
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Verify required flags are used
	flag.Parse()
	if ptr.Deref(file, "") == "" ||
		ptr.Deref(env, "") == "" ||
		ptr.Deref(oldTag, "") == "" ||
		ptr.Deref(newTag, "") == "" ||
		ptr.Deref(newCommit, "") == "" {
		log.Fatalf("flags - file, env, old-tag, new-tag, new-commit - are required")
	}

	// Verify the right env value was used
	if !isValidEnvironment(*env) {
		log.Fatalf("invalid environment: %s", *env)
	}
	log.Print("Updating deployment spec for environment: ", *env)

	if ptr.Deref(jiraTicket, "") != "" {
		autoCommitChanges = true

		err := gitCheckout(ctx, filepath.Dir(*file), "master")
		if err != nil {
			log.Fatalf("failed to checkout the master branch: %v", err)
		}

		branchName = *jiraTicket + "-" + *env

		log.Printf("Creating git branch: %s", branchName)
		err = gitCreateBranch(ctx, filepath.Dir(*file), branchName)
		if err != nil {
			log.Fatalf("failed to create branch: %v", err)
		}
	}

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

	if autoCommitChanges {
		// Add the changes in git
		log.Printf("Adding changes in git")
		err = gitAdd(ctx, filepath.Dir(*file), *file)
		if err != nil {
			log.Fatalf("Error adding changes in git: %v", err)
		}

		// Generate MR title and description
		log.Printf("Generating merge request title and description")

		// Output the changes between tags for the merge request
		cmd := exec.Command("go", "run", "../release/notes.go", "--from="+*oldTag, "--to="+*newTag, "--token="+*token)
		cmdOutput, err := cmd.CombinedOutput()
		if err != nil {
			log.Fatalf("Error : %v", string(cmdOutput))
		}

		// Attempt to remove any control plane changes from the release notes
		var mergeRequestDescription string
		mergeRequestStrings := strings.Split(string(cmdOutput), "area/hypershift-operator")

		// It's possible there were only HO changes and if so, just use the command output
		if len(mergeRequestStrings) < 2 {
			mergeRequestDescription = string(cmdOutput)
		} else {
			mergeRequestDescription = "## area/hypershift-operator" + "\n" + mergeRequestStrings[1]
		}
		mergeRequestDescription = mergeRequestDescription + "\\n/hold"
		log.Printf(mergeRequestDescription)

		// Commit the changes in git
		log.Printf("Committing changes in git")
		mergeRequestTitle := fmt.Sprintf("%s: Bump HyperShift in %s to %s / %s", *jiraTicket, *env, *newTag, *newCommit)
		err = gitCommit(ctx, filepath.Dir(*file), mergeRequestTitle, mergeRequestDescription)
		if err != nil {
			log.Fatalf("Error committing changes in git: %v", err)
		}

		// Push the changes up and create the merge request
		log.Printf("Pushing changes in git and creating merge request")
		err = gitPushAndCreateMergeRequest(ctx, filepath.Dir(*file), ptr.Deref(remote, ""), branchName, mergeRequestTitle, strings.Replace(mergeRequestDescription, "\n", "\\n", -1))
		if err != nil {
			log.Fatalf("Error pushing changes in git: %v", err)
		}
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

func gitCheckout(ctx context.Context, repoPath, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "checkout", branch)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error checking out branch: %s\n%s", err, output)
	}
	fmt.Printf("Output: %s\n", output)
	return nil
}

func gitCreateBranch(ctx context.Context, repoPath, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "checkout", "-b", branch)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error creating branch: %s\n%s", err, output)
	}
	fmt.Printf("Output: %s\n", output)
	return nil
}

func gitAdd(ctx context.Context, repoPath, fileToAdd string) error {
	cmd := exec.CommandContext(ctx, "git", "add", fileToAdd)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error adding files: %s\n%s", err, output)
	}
	fmt.Printf("Output: %s\n", output)
	return nil
}

func gitCommit(ctx context.Context, repoPath, message, description string) error {
	cmd := exec.CommandContext(ctx, "git", "commit", "-m", message, "-m", description, "--signoff")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error committing files: %s\n%s", err, output)
	}
	fmt.Printf("Output: %s\n", output)
	return nil
}

func gitPushAndCreateMergeRequest(ctx context.Context, repoPath, remote, branch, mergeRequestTitle, mergeRequestDescription string) error {
	cmd := exec.CommandContext(ctx, "git", "push", remote, branch, "-o merge_request.create", fmt.Sprintf("-o merge_request.title=%s", mergeRequestTitle), fmt.Sprintf("-o merge_request.description=%s", mergeRequestDescription))
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error pushing to remote: %s\n%s", err, output)
	}
	fmt.Printf("Output: %s\n", output)
	return nil
}
