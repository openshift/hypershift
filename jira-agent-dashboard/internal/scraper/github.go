package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const defaultGitHubBaseURL = "https://api.github.com"

// PRInfo holds metadata about a GitHub pull request.
type PRInfo struct {
	State        string     // open / closed
	Merged       bool
	CreatedAt    *time.Time
	MergedAt     *time.Time
	ClosedAt     *time.Time
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

// TokenSource provides a GitHub access token, potentially refreshing it.
type TokenSource interface {
	Token() (string, error)
}

// staticToken is a TokenSource that returns a fixed token.
type staticToken struct{ token string }

func (s *staticToken) Token() (string, error) { return s.token, nil }

// GitHubClient provides access to the GitHub REST API v3.
type GitHubClient struct {
	tokenSource TokenSource
	httpClient  *http.Client
	baseURL     string
}

// NewGitHubClient creates a GitHubClient with the given personal access token.
// If token is empty, requests are made without authentication.
func NewGitHubClient(token string) *GitHubClient {
	return &GitHubClient{
		tokenSource: &staticToken{token: token},
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		baseURL:     defaultGitHubBaseURL,
	}
}

// NewGitHubClientWithApp creates a GitHubClient that authenticates via a GitHub App.
func NewGitHubClientWithApp(appAuth *GitHubAppAuth) *GitHubClient {
	return &GitHubClient{
		tokenSource: appAuth,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		baseURL:     defaultGitHubBaseURL,
	}
}

// ghPRResponse is the JSON structure returned by the GitHub PR endpoint.
type ghPRResponse struct {
	State        string     `json:"state"`
	Merged       bool       `json:"merged"`
	CreatedAt    *time.Time `json:"created_at"`
	MergedAt     *time.Time `json:"merged_at"`
	ClosedAt     *time.Time `json:"closed_at"`
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
	if err := c.setHeaders(req); err != nil {
		return nil, err
	}

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
		CreatedAt:    pr.CreatedAt,
		MergedAt:     pr.MergedAt,
		ClosedAt:     pr.ClosedAt,
		Additions:    pr.Additions,
		Deletions:    pr.Deletions,
		ChangedFiles: pr.ChangedFiles,
	}, nil
}

// GetPRReviewComments fetches all comments for a pull request — both inline
// review comments (pulls/{n}/comments) and issue-level conversation comments
// (issues/{n}/comments) — and returns them combined.
func (c *GitHubClient) GetPRReviewComments(ctx context.Context, owner, repo string, number int) ([]CommentInfo, error) {
	review, err := c.fetchComments(ctx, fmt.Sprintf("%s/repos/%s/%s/pulls/%d/comments?per_page=100", c.baseURL, owner, repo, number))
	if err != nil {
		return nil, fmt.Errorf("fetching review comments: %w", err)
	}
	issue, err := c.fetchComments(ctx, fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=100", c.baseURL, owner, repo, number))
	if err != nil {
		return nil, fmt.Errorf("fetching issue comments: %w", err)
	}
	return append(review, issue...), nil
}

// fetchComments fetches all comments from a paginated GitHub API endpoint.
func (c *GitHubClient) fetchComments(ctx context.Context, url string) ([]CommentInfo, error) {

	var allComments []CommentInfo
	for url != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("creating comments request: %w", err)
		}
		if err := c.setHeaders(req); err != nil {
			return nil, err
		}

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

func (c *GitHubClient) setHeaders(req *http.Request) error {
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if c.tokenSource != nil {
		token, err := c.tokenSource.Token()
		if err != nil {
			return fmt.Errorf("getting token: %w", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	return nil
}

// allowedBots is the list of bot authors whose comments are kept.
var allowedBots = map[string]bool{
	"coderabbitai[bot]": true,
}

// noiseBodyPatterns are substrings that mark a comment as noise even from allowed bots.
var noiseBodyPatterns = []string{
	"No actionable comments were generated",
	"Skipped: comment is from another GitHub bot",
	"<!-- walkthrough_start -->",
	"skip review by coderabbit.ai",
}

// slashCommandRe matches a body that starts (after whitespace) with /word.
var slashCommandRe = regexp.MustCompile(`(?s)^\s*/[a-z]`)

// IsNoiseComment returns true if the comment should be filtered out.
func IsNoiseComment(author, body string) bool {
	// Filter bot authors not on the allowlist.
	if strings.HasSuffix(author, "[bot]") || strings.HasSuffix(author, "-robot") || author == "cwbotbot" {
		if !allowedBots[author] {
			return true
		}
	}
	// Filter slash commands (/lgtm, /test, /approve, etc.).
	if slashCommandRe.MatchString(body) {
		return true
	}
	// Filter noise phrases from any author.
	for _, pattern := range noiseBodyPatterns {
		if strings.Contains(body, pattern) {
			return true
		}
	}
	return false
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
