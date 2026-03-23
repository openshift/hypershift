package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

const defaultGitHubBaseURL = "https://api.github.com"

// PRInfo holds metadata about a GitHub pull request.
type PRInfo struct {
	State        string     // open / closed
	Merged       bool
	MergedAt     *time.Time
	Additions    int
	Deletions    int
	ChangedFiles int
}

// CommentInfo holds metadata about a single PR review comment.
type CommentInfo struct {
	ID        int64
	Author    string
	Body      string
	CreatedAt time.Time
}

// GitHubClient provides access to the GitHub REST API v3.
type GitHubClient struct {
	token      string
	httpClient *http.Client
	baseURL    string
}

// NewGitHubClient creates a GitHubClient with the given personal access token.
// If token is empty, requests are made without authentication.
func NewGitHubClient(token string) *GitHubClient {
	return &GitHubClient{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultGitHubBaseURL,
	}
}

// ghPRResponse is the JSON structure returned by the GitHub PR endpoint.
type ghPRResponse struct {
	State        string     `json:"state"`
	Merged       bool       `json:"merged"`
	MergedAt     *time.Time `json:"merged_at"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	ChangedFiles int        `json:"changed_files"`
}

// ghCommentResponse is the JSON structure for a single review comment.
type ghCommentResponse struct {
	ID        int64  `json:"id"`
	User      ghUser `json:"user"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type ghUser struct {
	Login string `json:"login"`
}

// GetPR fetches pull request info from the GitHub API.
func (c *GitHubClient) GetPR(ctx context.Context, owner, repo string, number int) (*PRInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", c.baseURL, owner, repo, number)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating PR request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching PR: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching PR: HTTP %d", resp.StatusCode)
	}

	var pr ghPRResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding PR response: %w", err)
	}

	return &PRInfo{
		State:        pr.State,
		Merged:       pr.Merged,
		MergedAt:     pr.MergedAt,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		ChangedFiles: pr.ChangedFiles,
	}, nil
}

// GetPRReviewComments fetches all review comments for a pull request,
// following pagination via the Link header.
func (c *GitHubClient) GetPRReviewComments(ctx context.Context, owner, repo string, number int) ([]CommentInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/comments?per_page=100", c.baseURL, owner, repo, number)

	var allComments []CommentInfo
	for url != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("creating comments request: %w", err)
		}
		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetching comments: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("fetching comments: HTTP %d", resp.StatusCode)
		}

		var comments []ghCommentResponse
		if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding comments response: %w", err)
		}
		resp.Body.Close()

		for _, c := range comments {
			createdAt, err := time.Parse(time.RFC3339, c.CreatedAt)
			if err != nil {
				return nil, fmt.Errorf("parsing comment created_at %q: %w", c.CreatedAt, err)
			}
			allComments = append(allComments, CommentInfo{
				ID:        c.ID,
				Author:    c.User.Login,
				Body:      c.Body,
				CreatedAt: createdAt,
			})
		}

		url = parseNextLink(resp.Header.Get("Link"))
	}

	return allComments, nil
}

func (c *GitHubClient) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// linkNextRe matches the "next" relation in a Link header.
var linkNextRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// parseNextLink extracts the URL for rel="next" from a GitHub Link header.
// Returns empty string if there is no next page.
func parseNextLink(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}
	matches := linkNextRe.FindStringSubmatch(linkHeader)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}
