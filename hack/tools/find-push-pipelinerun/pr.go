package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var githubAPIBase = "https://api.github.com" //nolint:gochecknoglobals

func setGitHubAPIBase(base string) { githubAPIBase = base }

// PRRef represents a resolved pull request reference.
type PRRef struct {
	Repo   string // "owner/repo"
	Number int
}

var (
	githubURLRe    = regexp.MustCompile(`^https?://github\.com/([^/]+/[^/]+)/pull/(\d+)/?$`)
	ownerRepoNumRe = regexp.MustCompile(`^([a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+)#(\d+)$`)
	gitRemoteRe    = regexp.MustCompile(`github\.com[:/]([^/]+/[^/.\s]+?)(?:\.git)?$`)
)

// ResolvePR parses a PR reference string into its components.
// Accepted formats:
//   - GitHub URL: https://github.com/owner/repo/pull/123
//   - owner/repo#123
//   - bare number (infers repo from git remote)
func ResolvePR(input string) (*PRRef, error) {
	if input == "" {
		return nil, fmt.Errorf("empty PR reference")
	}

	if m := githubURLRe.FindStringSubmatch(input); m != nil {
		num, _ := strconv.Atoi(m[2])
		return &PRRef{Repo: m[1], Number: num}, nil
	}

	if m := ownerRepoNumRe.FindStringSubmatch(input); m != nil {
		num, _ := strconv.Atoi(m[2])
		return &PRRef{Repo: m[1], Number: num}, nil
	}

	num, err := strconv.Atoi(input)
	if err != nil {
		return nil, fmt.Errorf("unrecognized PR reference: %q", input)
	}

	repo, err := inferRepo()
	if err != nil {
		return nil, fmt.Errorf("inferring repo for PR %d: %w", num, err)
	}
	return &PRRef{Repo: repo, Number: num}, nil
}

func inferRepo() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output() //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("reading git remote: %w", err)
	}
	m := gitRemoteRe.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return "", fmt.Errorf("cannot parse GitHub owner/repo from remote URL: %s", strings.TrimSpace(string(out)))
	}
	return m[1], nil
}

// GetMergeSHA returns the merge commit SHA for a merged PR.
// Uses the GitHub REST API directly. Requires GITHUB_TOKEN to be set for
// private repos or to avoid rate limits.
func GetMergeSHA(client *http.Client, ref *PRRef) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%d", githubAPIBase, ref.Repo, ref.Number)
	req, err := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("querying GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d for %s", resp.StatusCode, url)
	}

	var pr struct {
		State          string `json:"state"`
		Merged         bool   `json:"merged"`
		MergeCommitSHA string `json:"merge_commit_sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", fmt.Errorf("parsing GitHub API response: %w", err)
	}

	if !pr.Merged {
		return "", fmt.Errorf("PR %s#%d is not merged (state: %s)", ref.Repo, ref.Number, pr.State)
	}
	if pr.MergeCommitSHA == "" {
		return "", fmt.Errorf("PR %s#%d has no merge commit", ref.Repo, ref.Number)
	}

	return pr.MergeCommitSHA, nil
}

// newGitHubClient creates an HTTP client for the GitHub API.
// If GITHUB_TOKEN is set, requests include authorization.
func newGitHubClient(token string) *http.Client {
	if token == "" {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &tokenTransport{token: token, base: http.DefaultTransport},
	}
}

type tokenTransport struct {
	token string
	base  http.RoundTripper
}

func (t *tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}
