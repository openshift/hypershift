package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

type PullRequest struct {
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Title string `json:"title"`
	Url   string `json:"html_url"`
}

type commit struct {
	sha  string
	prID string
}

var (
	fromTag = flag.String("from", "", "The tag or commit to start from.")
	toTag   = flag.String("to", "", "The tag or commit to compare with.")
)

func main() {
	flag.Parse()

	if fromTag == nil || toTag == nil ||
		*fromTag == "" || *toTag == "" {
		fmt.Println("--from and --to are mandatory")
		os.Exit(1)
	}
	// Set the path to the Git repository
	repoPath := "."

	// Set the hashes of the two commits to compare
	fmt.Println(fmt.Sprintf("Finding PRs between %q and %q", *fromTag, *toTag))

	// Run the Git command to get the commit log between the two commits
	cmd := exec.Command("git", "-C", repoPath, "log", "--pretty=format:%H %s", fmt.Sprintf("%s..%s", *fromTag, *toTag), "--merges")
	output, err := cmd.Output()
	if err != nil {
		panic(err)
	}

	// Split the output into individual commit messages
	outLines := strings.Split(string(output), "\n")
	fmt.Println(fmt.Sprintf("Found %v PRs", len(outLines)))

	// Print out the commit messages
	commits := make([]commit, 0)
	for _, l := range outLines {
		// Format is "71b43357bd48014783a94ab2eca6ae45474268d1 Merge pull request #2103 from sjenning/skip-ovnkube-pod-restart-check"
		sha, body, found := strings.Cut(l, " ")
		if !found {
			continue
		}
		_, body, found = strings.Cut(body, "#")
		if !found {
			continue
		}
		prID, _, found := strings.Cut(body, " ")
		if !found {
			continue
		}

		c := commit{
			sha:  sha,
			prID: prID,
		}
		commits = append(commits, c)
	}

	pullRequests := make([]PullRequest, 0)
	for _, c := range commits {
		// We have to make an API call per PR because GH API doesn't let you list between two commits
		// https://docs.github.com/en/rest/pulls/pulls#list-pull-requests
		url := fmt.Sprintf("https://api.github.com/repos/openshift/hypershift/pulls/%s", c.prID)

		// Set up the API request headers with a personal access token.
		token := os.Getenv("GITHUB_ACCESS_TOKEN")
		if token == "" {
			fmt.Println("Please set the GITHUB_ACCESS_TOKEN environment variable with a personal access token.")
			return
		}
		headers := map[string]string{
			"Accept":        "application/vnd.github.v3+json",
			"Authorization": fmt.Sprintf("Bearer %s", token),
		}

		// Make the API request.
		resp, err := makeRequest("GET", url, headers, nil)
		if err != nil {
			fmt.Printf("Error making API request: %v", err)
			return
		}

		if resp.StatusCode != 200 {
			fmt.Println(fmt.Sprintf("API returned: %v", resp.StatusCode))
		}

		// Parse the API response body.
		pullRequest := &PullRequest{}
		err = json.NewDecoder(resp.Body).Decode(pullRequest)
		if err != nil {
			fmt.Printf("Error decoding API response: %v", err)
			return
		}
		pullRequests = append(pullRequests, *pullRequest)
	}

	// Group the merged pull requests by label.
	labels := make(map[string][]PullRequest)
	for _, pr := range pullRequests {
		for _, label := range pr.Labels {
			if label.Name != "area/control-plane-operator" && label.Name != "area/hypershift-operator" {
				continue
			}
			labels[label.Name] = append(labels[label.Name], pr)
		}
	}

	// Generate the Markdown-formatted changelog.
	for label, pullRequests := range labels {
		fmt.Printf("## %s\n\n", label)
		for _, pr := range pullRequests {
			fmt.Printf("- [%s](%s)\n", pr.Title, pr.Url)
		}
		fmt.Println()
	}
}

func makeRequest(method string, url string, headers map[string]string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
