package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

// pullsCommentsRe matches the pulls review comments endpoint path.
var pullsCommentsRe = regexp.MustCompile(`/pulls/\d+/comments`)

func TestParsePRState(t *testing.T) {
	tests := []struct {
		name         string
		state        string
		merged       bool
		mergedAt     *string
		wantState    string
		wantMerged   bool
		wantMergedAt bool
	}{
		{
			name:       "When PR is open it should report open state",
			state:      "open",
			merged:     false,
			mergedAt:   nil,
			wantState:  "open",
			wantMerged: false,
		},
		{
			name:         "When PR is merged it should report closed state with merged true",
			state:        "closed",
			merged:       true,
			mergedAt:     strPtr("2025-01-15T10:30:00Z"),
			wantState:    "closed",
			wantMerged:   true,
			wantMergedAt: true,
		},
		{
			name:       "When PR is closed without merge it should report closed state with merged false",
			state:      "closed",
			merged:     false,
			mergedAt:   nil,
			wantState:  "closed",
			wantMerged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := map[string]interface{}{
				"state":         tt.state,
				"merged":        tt.merged,
				"additions":     10,
				"deletions":     5,
				"changed_files": 3,
			}
			if tt.mergedAt != nil {
				response["merged_at"] = *tt.mergedAt
			}

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			}))
			defer srv.Close()

			client := NewGitHubClient("")
			client.baseURL = srv.URL

			pr, err := client.GetPR(context.Background(), "owner", "repo", 1)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pr.State != tt.wantState {
				t.Errorf("State = %q, want %q", pr.State, tt.wantState)
			}
			if pr.Merged != tt.wantMerged {
				t.Errorf("Merged = %v, want %v", pr.Merged, tt.wantMerged)
			}
			if tt.wantMergedAt && pr.MergedAt == nil {
				t.Error("MergedAt is nil, want non-nil")
			}
			if !tt.wantMergedAt && pr.MergedAt != nil {
				t.Errorf("MergedAt = %v, want nil", pr.MergedAt)
			}
		})
	}
}

func TestParseDiffStats(t *testing.T) {
	response := map[string]interface{}{
		"state":         "open",
		"merged":        false,
		"additions":     142,
		"deletions":     37,
		"changed_files": 8,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	client := NewGitHubClient("")
	client.baseURL = srv.URL

	pr, err := client.GetPR(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr.Additions != 142 {
		t.Errorf("Additions = %d, want 142", pr.Additions)
	}
	if pr.Deletions != 37 {
		t.Errorf("Deletions = %d, want 37", pr.Deletions)
	}
	if pr.ChangedFiles != 8 {
		t.Errorf("ChangedFiles = %d, want 8", pr.ChangedFiles)
	}
}

func TestParseReviewComments(t *testing.T) {
	reviewComments := []map[string]interface{}{
		{
			"id":         1001,
			"user":       map[string]interface{}{"login": "reviewer1"},
			"body":       "Looks good, but consider refactoring this.",
			"created_at": "2025-02-10T08:15:00Z",
		},
	}
	issueComments := []map[string]interface{}{
		{
			"id":         1002,
			"user":       map[string]interface{}{"login": "reviewer2"},
			"body":       "Nit: missing error check.",
			"created_at": "2025-02-10T09:30:00Z",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if pullsCommentsRe.MatchString(r.URL.Path) {
			json.NewEncoder(w).Encode(reviewComments)
		} else {
			json.NewEncoder(w).Encode(issueComments)
		}
	}))
	defer srv.Close()

	client := NewGitHubClient("")
	client.baseURL = srv.URL

	result, err := client.GetPRReviewComments(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("got %d comments, want 2", len(result))
	}

	if result[0].ID != 1001 {
		t.Errorf("comment[0].ID = %d, want 1001", result[0].ID)
	}
	if result[0].Author != "reviewer1" {
		t.Errorf("comment[0].Author = %q, want %q", result[0].Author, "reviewer1")
	}
	if result[0].Body != "Looks good, but consider refactoring this." {
		t.Errorf("comment[0].Body = %q, want expected body", result[0].Body)
	}
	expectedTime, _ := time.Parse(time.RFC3339, "2025-02-10T08:15:00Z")
	if !result[0].CreatedAt.Equal(expectedTime) {
		t.Errorf("comment[0].CreatedAt = %v, want %v", result[0].CreatedAt, expectedTime)
	}

	if result[1].ID != 1002 {
		t.Errorf("comment[1].ID = %d, want 1002", result[1].ID)
	}
	if result[1].Author != "reviewer2" {
		t.Errorf("comment[1].Author = %q, want %q", result[1].Author, "reviewer2")
	}
}

func TestParseReviewCommentsPaginated(t *testing.T) {
	page1 := []map[string]interface{}{
		{
			"id":         2001,
			"user":       map[string]interface{}{"login": "alice"},
			"body":       "First page comment",
			"created_at": "2025-03-01T10:00:00Z",
		},
	}
	page2 := []map[string]interface{}{
		{
			"id":         2002,
			"user":       map[string]interface{}{"login": "bob"},
			"body":       "Second page comment",
			"created_at": "2025-03-01T11:00:00Z",
		},
	}

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return empty for issue comments endpoint
		if !pullsCommentsRe.MatchString(r.URL.Path) {
			json.NewEncoder(w).Encode([]map[string]interface{}{})
			return
		}
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=2>; rel="next"`, srvURL, r.URL.Path))
			json.NewEncoder(w).Encode(page1)
		} else {
			json.NewEncoder(w).Encode(page2)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	client := NewGitHubClient("")
	client.baseURL = srv.URL

	result, err := client.GetPRReviewComments(context.Background(), "owner", "repo", 99)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("got %d comments, want 2", len(result))
	}
	if result[0].ID != 2001 {
		t.Errorf("comment[0].ID = %d, want 2001", result[0].ID)
	}
	if result[1].ID != 2002 {
		t.Errorf("comment[1].ID = %d, want 2002", result[1].ID)
	}
}

func TestIsNoiseComment(t *testing.T) {
	tests := []struct {
		name   string
		author string
		body   string
		want   bool
	}{
		{"When author is excluded bot it should be noise", "openshift-ci[bot]", "some text", true},
		{"When author is excluded robot it should be noise", "openshift-ci-robot", "some text", true},
		{"When author is allowed bot it should not be noise", "coderabbitai[bot]", "real review content", false},
		{"When body is slash command it should be noise", "bryan-cox", "/lgtm", true},
		{"When body is slash command with args it should be noise", "bryan-cox", "/test e2e-aws", true},
		{"When body has leading newline slash command it should be noise", "bryan-cox", "\n/approve", true},
		{"When body has text before slash it should not be noise", "sjenning", "good analysis\n/override ci/prow/e2e", false},
		{"When body is no actionable comments it should be noise", "coderabbitai[bot]", "No actionable comments were generated", true},
		{"When body is skipped bot comment it should be noise", "coderabbitai[bot]", "Skipped: comment is from another GitHub bot", true},
		{"When body is coderabbit walkthrough it should be noise", "coderabbitai[bot]", "<!-- This is an auto-generated comment: summarize by coderabbit.ai -->\n<!-- walkthrough_start -->\n\n## Walkthrough\n\nIntroduces TLS cert rotation", true},
		{"When body is coderabbit review skipped it should be noise", "coderabbitai[bot]", "<!-- This is an auto-generated comment: skip review by coderabbit.ai -->\n\n> Review skipped", true},
		{"When body is coderabbit actual review it should not be noise", "coderabbitai[bot]", "_⚠️ Potential issue_ | _🔴 Critical_\n\nThe ClusterRoleBinding should use a different subject", false},
		{"When body is real human comment it should not be noise", "bryan-cox", "Why not use NewARMClientOptions here?", false},
		{"When author is cwbotbot it should be noise", "cwbotbot", "some text", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNoiseComment(tt.author, tt.body)
			if got != tt.want {
				t.Errorf("IsNoiseComment(%q, %q) = %v, want %v", tt.author, tt.body, got, tt.want)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
