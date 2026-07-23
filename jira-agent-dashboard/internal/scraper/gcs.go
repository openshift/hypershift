package scraper

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	gcsBaseURL = "https://storage.googleapis.com/test-platform-results"
	gcsBucket  = "test-platform-results"
)

// JobConfig defines a periodic job's GCS layout.
type JobConfig struct {
	Name      string // e.g. "hypershift", "installer"
	JobPrefix string // GCS directory prefix under the bucket
	StepPath  string // step artifact path within each build
}

// DefaultJobConfigs returns the set of jira-agent periodic jobs to scrape.
func DefaultJobConfigs() []JobConfig {
	return []JobConfig{
		{Name: "hypershift", JobPrefix: "logs/periodic-ci-openshift-hypershift-main-periodic-jira-agent/", StepPath: "artifacts/periodic-jira-agent/hypershift-jira-agent-process/"},
		{Name: "installer", JobPrefix: "logs/periodic-ci-openshift-installer-main-periodic-jira-agent/", StepPath: "artifacts/periodic-jira-agent/jira-agent-process/"},
	}
}

// GCSClient abstracts access to GCS so it can be mocked in tests.
type GCSClient interface {
	ListBuilds(ctx context.Context) ([]string, error)
	ReadFile(ctx context.Context, buildID, path string) ([]byte, error)
	ReadBuildFile(ctx context.Context, buildID, path string) ([]byte, error)
	ListBuildArtifacts(ctx context.Context, buildID string) ([]string, error)
}

// PhaseTokens holds per-phase token usage and cost data extracted from build-log.txt.
type PhaseTokens struct {
	PhaseName                string
	PhaseNumber              int
	TotalCostUSD             float64 `json:"total_cost_usd"`
	DurationMs               int64   `json:"duration_ms"`
	NumTurns                 int     `json:"num_turns"`
	InputTokens              int64   `json:"input_tokens"`
	OutputTokens             int64   `json:"output_tokens"`
	CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
	Model                    string  `json:"model"`
}

// BuildLogResult holds all data extracted from a single build-log.txt.
type BuildLogResult struct {
	IssueKey string
	PRURL    string
	Phases   []PhaseTokens
}

// HTTPGCSClient implements GCSClient using the public GCS HTTP endpoints.
type HTTPGCSClient struct {
	Client    *http.Client
	JobPrefix string
	StepPath  string
}

// gcsListResponse is the JSON structure returned by the GCS JSON API for object listing.
type gcsListResponse struct {
	Prefixes []string `json:"prefixes"`
}

// NewHTTPGCSClient creates an HTTPGCSClient configured for a specific job.
// If client is nil, a client with a 30-second timeout is used.
func NewHTTPGCSClient(client *http.Client, jobPrefix, stepPath string) *HTTPGCSClient {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPGCSClient{Client: client, JobPrefix: jobPrefix, StepPath: stepPath}
}

// ListBuilds returns build IDs by listing directory prefixes under the job path
// using the GCS JSON API.
func (c *HTTPGCSClient) ListBuilds(ctx context.Context) ([]string, error) {
	url := fmt.Sprintf(
		"https://storage.googleapis.com/storage/v1/b/%s/o?prefix=%s&delimiter=/",
		gcsBucket, c.JobPrefix,
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
		trimmed := strings.TrimPrefix(prefix, c.JobPrefix)
		trimmed = strings.TrimSuffix(trimmed, "/")
		if trimmed != "" {
			builds = append(builds, trimmed)
		}
	}
	return builds, nil
}

// ReadFile fetches a file for the given build ID and relative path within the step directory.
// For example, path="build-log.txt" reads the build log, and path="artifacts/foo.txt"
// reads from the artifacts subdirectory.
func (c *HTTPGCSClient) ReadFile(ctx context.Context, buildID, path string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s%s/%s%s", gcsBaseURL, c.JobPrefix, buildID, c.StepPath, path)

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
	return maybeGunzip(data), nil
}

// maybeGunzip decompresses data if it starts with the gzip magic bytes.
// GCS objects stored with Content-Encoding: gzip may arrive compressed
// depending on proxy and transport configuration.
func maybeGunzip(data []byte) []byte {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return data
	}
	return out
}

// ReadBuildFile fetches a file at the build root level (e.g., started.json, finished.json).
func (c *HTTPGCSClient) ReadBuildFile(ctx context.Context, buildID, path string) ([]byte, error) {
	url := fmt.Sprintf("%s/%s%s/%s", gcsBaseURL, c.JobPrefix, buildID, path)

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

// ListBuildArtifacts returns filenames in the step artifacts subdirectory for a build.
func (c *HTTPGCSClient) ListBuildArtifacts(ctx context.Context, buildID string) ([]string, error) {
	prefix := fmt.Sprintf("%s%s/%sartifacts/", c.JobPrefix, buildID, c.StepPath)
	listURL := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o?prefix=%s&delimiter=", gcsBucket, prefix)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating list request: %w", err)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing artifacts: HTTP %d", resp.StatusCode)
	}

	var listResp struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("decoding artifact list: %w", err)
	}

	var names []string
	for _, item := range listResp.Items {
		name := strings.TrimPrefix(item.Name, prefix)
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// Regex patterns for parsing build-log.txt
var (
	processingRe    = regexp.MustCompile(`^Processing:\s+(\S+)`)
	prCreatedRe     = regexp.MustCompile(`PR created:\s+(https://github\.com/\S+/pull/\d+)`)
	prLineRe        = regexp.MustCompile(`^\s+PR:\s+(https://github\.com/\S+/pull/\d+)`)
	phaseTokensRe   = regexp.MustCompile(`^Phase\s+(\d+)\s+tokens:\s*\{`)
	phaseCompleteRe = regexp.MustCompile(`Phase\s+\d+\s+\(([^)]+)\)\s+completed\s+for`)
)

// phaseNameMap maps the parenthesized name from "Phase N (name) completed" to DB phase names.
var phaseNameMap = map[string]string{
	"jira-solve":        "solve",
	"pre-commit review": "review",
	"address review":    "fix",
	"create-pr":         "pr",
}

// ParseBuildLog parses a build-log.txt and extracts issue key, PR URL, and per-phase token data.
func ParseBuildLog(data []byte) (*BuildLogResult, error) {
	result := &BuildLogResult{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Increase scanner buffer for long JSON lines
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	// Track phase names from "Phase N (name) completed" lines
	phaseNames := make(map[int]string)

	for scanner.Scan() {
		line := scanner.Text()

		// Extract issue key from "Processing: OCPBUGS-79071"
		if m := processingRe.FindStringSubmatch(line); m != nil && result.IssueKey == "" {
			result.IssueKey = m[1]
			continue
		}

		// Extract PR URL from "✅ PR created: https://..." or "   PR: https://..."
		if m := prCreatedRe.FindStringSubmatch(line); m != nil && result.PRURL == "" {
			result.PRURL = m[1]
			continue
		}
		if m := prLineRe.FindStringSubmatch(line); m != nil && result.PRURL == "" {
			result.PRURL = m[1]
			continue
		}

		// Extract phase name from "Phase N (phase-name) completed for ISSUE"
		if m := phaseCompleteRe.FindStringSubmatch(line); m != nil {
			// Extract phase number
			var phaseNum int
			fmt.Sscanf(line, "Phase %d", &phaseNum) //nolint:errcheck
			if phaseNum > 0 {
				if mapped, ok := phaseNameMap[m[1]]; ok {
					phaseNames[phaseNum] = mapped
				} else {
					phaseNames[phaseNum] = m[1]
				}
			}
			continue
		}

		// Extract phase token blocks: "Phase N tokens: {"
		if m := phaseTokensRe.FindStringSubmatch(line); m != nil {
			var phaseNum int
			fmt.Sscanf(m[1], "%d", &phaseNum) //nolint:errcheck

			// Collect the JSON block, tracking brace depth for nested objects
			var jsonBuf bytes.Buffer
			jsonBuf.WriteString("{\n")
			depth := 1
			for scanner.Scan() {
				jsonLine := scanner.Text()
				jsonBuf.WriteString(jsonLine)
				jsonBuf.WriteString("\n")
				for _, ch := range jsonLine {
					if ch == '{' {
						depth++
					} else if ch == '}' {
						depth--
					}
				}
				if depth == 0 {
					break
				}
			}

			var tokens PhaseTokens
			if err := json.Unmarshal(jsonBuf.Bytes(), &tokens); err != nil {
				log.Printf("Warning: failed to parse phase %d token JSON: %v", phaseNum, err)
				continue
			}
			tokens.PhaseNumber = phaseNum
			if name, ok := phaseNames[phaseNum]; ok {
				tokens.PhaseName = name
			} else {
				switch phaseNum {
				case 1:
					tokens.PhaseName = "solve"
				case 2:
					tokens.PhaseName = "review"
				case 3:
					tokens.PhaseName = "fix"
				case 4:
					tokens.PhaseName = "pr"
				default:
					tokens.PhaseName = fmt.Sprintf("phase-%d", phaseNum)
				}
			}
			result.Phases = append(result.Phases, tokens)
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning build log: %w", err)
	}

	if result.IssueKey == "" {
		return nil, fmt.Errorf("no issue key found in build log")
	}

	return result, nil
}
