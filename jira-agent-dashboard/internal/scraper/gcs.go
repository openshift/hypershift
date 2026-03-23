package scraper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	gcsBaseURL   = "https://storage.googleapis.com/test-platform-results"
	gcsBucket    = "test-platform-results"
	jobPrefix    = "logs/periodic-ci-openshift-hypershift-main-periodic-jira-agent/"
	artifactPath = "artifacts/periodic-jira-agent/hypershift-jira-agent-process/artifacts/"
)

// GCSClient abstracts access to GCS so it can be mocked in tests.
type GCSClient interface {
	ListBuilds(ctx context.Context) ([]string, error)
	ReadFile(ctx context.Context, buildID, path string) ([]byte, error)
}

// ProcessedIssue represents a single line from processed-issues.txt.
type ProcessedIssue struct {
	IssueKey  string
	Timestamp string
	PRURL     string
	Status    string
}

// TokenUsage represents the token/cost JSON produced per phase.
type TokenUsage struct {
	TotalCostUSD             float64 `json:"total_cost_usd"`
	DurationMs               int64   `json:"duration_ms"`
	NumTurns                 int     `json:"num_turns"`
	InputTokens              int64   `json:"input_tokens"`
	OutputTokens             int64   `json:"output_tokens"`
	CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
	Model                    string  `json:"model"`
}

// HTTPGCSClient implements GCSClient using the public GCS HTTP endpoints.
type HTTPGCSClient struct {
	Client *http.Client
}

// gcsListResponse is the JSON structure returned by the GCS JSON API for object listing.
type gcsListResponse struct {
	Prefixes []string `json:"prefixes"`
}

// NewHTTPGCSClient creates an HTTPGCSClient with the given http.Client.
// If client is nil, http.DefaultClient is used.
func NewHTTPGCSClient(client *http.Client) *HTTPGCSClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPGCSClient{Client: client}
}

// ListBuilds returns build IDs by listing directory prefixes under the job path
// using the GCS JSON API.
func (c *HTTPGCSClient) ListBuilds(ctx context.Context) ([]string, error) {
	url := fmt.Sprintf(
		"https://storage.googleapis.com/storage/v1/b/%s/o?prefix=%s&delimiter=/",
		gcsBucket, jobPrefix,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating list request: %w", err)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing builds: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing builds: HTTP %d", resp.StatusCode)
	}

	var listResp gcsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decoding list response: %w", err)
	}

	var builds []string
	for _, prefix := range listResp.Prefixes {
		// Prefixes look like "logs/periodic-ci-openshift-hypershift-main-periodic-jira-agent/1234567890/"
		trimmed := strings.TrimPrefix(prefix, jobPrefix)
		trimmed = strings.TrimSuffix(trimmed, "/")
		if trimmed != "" {
			builds = append(builds, trimmed)
		}
	}
	return builds, nil
}

// ReadFile fetches an artifact file for the given build ID and relative path.
func (c *HTTPGCSClient) ReadFile(ctx context.Context, buildID, path string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s%s/%s%s", gcsBaseURL, jobPrefix, buildID, artifactPath, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating read request: %w", err)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reading file %s: HTTP %d", path, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body for %s: %w", path, err)
	}
	return data, nil
}

// ParseProcessedIssues parses the contents of a processed-issues.txt file.
// Each non-empty line must have exactly 4 space-separated fields:
// ISSUE_KEY TIMESTAMP PR_URL STATUS
func ParseProcessedIssues(data []byte) ([]ProcessedIssue, error) {
	var issues []ProcessedIssue
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("line %d: expected 4 fields, got %d", lineNum, len(fields))
		}
		issues = append(issues, ProcessedIssue{
			IssueKey:  fields[0],
			Timestamp: fields[1],
			PRURL:     fields[2],
			Status:    fields[3],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning processed issues: %w", err)
	}
	return issues, nil
}

// ParseTokensJSON parses a claude-*-tokens.json file into a TokenUsage struct.
func ParseTokensJSON(data []byte) (*TokenUsage, error) {
	var usage TokenUsage
	if err := json.Unmarshal(data, &usage); err != nil {
		return nil, fmt.Errorf("parsing tokens JSON: %w", err)
	}
	return &usage, nil
}

// ParseDuration parses a duration text file containing a single integer (seconds).
func ParseDuration(data []byte) (int64, error) {
	s := strings.TrimSpace(string(data))
	if s == "" {
		return 0, fmt.Errorf("empty duration file")
	}
	d, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing duration: %w", err)
	}
	return d, nil
}
