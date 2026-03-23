//go:build integration

package internal_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openshift/jira-agent-dashboard/internal/api"
	"github.com/openshift/jira-agent-dashboard/internal/db"

	_ "github.com/mattn/go-sqlite3"
)

func TestIntegrationAPIFlow(t *testing.T) {
	// 1. Open in-memory SQLite database
	sqlDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer sqlDB.Close()

	// 2. Initialize schema
	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	// 3. Create store
	store := db.NewStore(sqlDB)

	// 4. Seed data
	if err := seedTestData(store); err != nil {
		t.Fatalf("failed to seed test data: %v", err)
	}

	// 5. Create server with temporary web directory
	srv := api.NewServer(store, t.TempDir())

	// 6. Create test server
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// 7. Test each endpoint
	t.Run("GET /api/trends", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/trends?from=2024-01-01&to=2024-12-31")
		if err != nil {
			t.Fatalf("failed to GET /api/trends: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var trends []api.TrendPoint
		if err := json.NewDecoder(resp.Body).Decode(&trends); err != nil {
			t.Fatalf("failed to decode trends response: %v", err)
		}

		if len(trends) == 0 {
			t.Error("expected at least 1 trend entry, got 0")
		}

		if len(trends) > 0 && trends[0].IssuesProcessed == 0 {
			t.Error("expected IssuesProcessed > 0, got 0")
		}
	})

	t.Run("GET /api/issues", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/api/issues?from=2024-01-01&to=2024-12-31")
		if err != nil {
			t.Fatalf("failed to GET /api/issues: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var issues []api.IssueSummary
		if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
			t.Fatalf("failed to decode issues response: %v", err)
		}

		if len(issues) != 3 {
			t.Errorf("expected 3 issues, got %d", len(issues))
		}

		if len(issues) > 0 {
			issue := issues[0]
			if issue.JiraKey == "" {
				t.Error("expected JiraKey to be non-empty")
			}
			if issue.PRState == "" {
				t.Error("expected PRState to be non-empty")
			}
			if issue.CostUSD == 0 {
				t.Error("expected CostUSD > 0, got 0")
			}
		}
	})

	t.Run("GET /api/issues/{id}", func(t *testing.T) {
		// Use issue ID 1 (first seeded issue)
		resp, err := http.Get(ts.URL + "/api/issues/1")
		if err != nil {
			t.Fatalf("failed to GET /api/issues/1: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var detail api.IssueDetail
		if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
			t.Fatalf("failed to decode issue detail response: %v", err)
		}

		if len(detail.Phases) != 4 {
			t.Errorf("expected 4 phases, got %d", len(detail.Phases))
		}

		if len(detail.Comments) != 2 {
			t.Errorf("expected 2 comments, got %d", len(detail.Comments))
		}
	})

	t.Run("GET /api/comments/{issueID}", func(t *testing.T) {
		// Get comments for issue ID 1
		resp, err := http.Get(ts.URL + "/api/comments/1")
		if err != nil {
			t.Fatalf("failed to GET /api/comments/1: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var comments []api.CommentDetail
		if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
			t.Fatalf("failed to decode comments response: %v", err)
		}

		if len(comments) != 2 {
			t.Errorf("expected 2 comments, got %d", len(comments))
		}
	})

	t.Run("PATCH /api/comments/{id}", func(t *testing.T) {
		// PATCH comment ID 1 with new classification
		update := api.ClassificationUpdate{
			Severity: "nitpick",
			Topic:    "style",
		}
		body, err := json.Marshal(update)
		if err != nil {
			t.Fatalf("failed to marshal update: %v", err)
		}

		req, err := http.NewRequest("PATCH", ts.URL+"/api/comments/1", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("failed to create PATCH request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to PATCH /api/comments/1: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var comment api.CommentDetail
		if err := json.NewDecoder(resp.Body).Decode(&comment); err != nil {
			t.Fatalf("failed to decode comment response: %v", err)
		}

		if !comment.HumanOverride {
			t.Error("expected HumanOverride to be true")
		}
		if comment.Severity != "nitpick" {
			t.Errorf("expected Severity 'nitpick', got '%s'", comment.Severity)
		}
		if comment.Topic != "style" {
			t.Errorf("expected Topic 'style', got '%s'", comment.Topic)
		}
	})
}

func seedTestData(store *db.Store) error {
	// Create 2 job runs
	jobRun1 := &db.JobRun{
		ProwJobID:   "prow-job-1",
		BuildID:     "build-1",
		StartedAt:   time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC),
		FinishedAt:  time.Date(2024, 3, 1, 11, 0, 0, 0, time.UTC),
		Status:      "success",
		ArtifactURL: "https://example.com/artifacts/1",
	}
	jobRunID1, err := store.InsertJobRun(jobRun1)
	if err != nil {
		return fmt.Errorf("failed to insert job run 1: %w", err)
	}

	jobRun2 := &db.JobRun{
		ProwJobID:   "prow-job-2",
		BuildID:     "build-2",
		StartedAt:   time.Date(2024, 3, 8, 10, 0, 0, 0, time.UTC),
		FinishedAt:  time.Date(2024, 3, 8, 11, 30, 0, 0, time.UTC),
		Status:      "success",
		ArtifactURL: "https://example.com/artifacts/2",
	}
	jobRunID2, err := store.InsertJobRun(jobRun2)
	if err != nil {
		return fmt.Errorf("failed to insert job run 2: %w", err)
	}

	// Create 3 issues
	mergedAt := time.Date(2024, 3, 2, 14, 0, 0, 0, time.UTC)
	mergeDuration := 28.0

	issue1 := &db.Issue{
		JobRunID:           jobRunID1,
		JiraKey:            "TEST-100",
		JiraURL:            "https://jira.example.com/browse/TEST-100",
		PRNumber:           100,
		PRURL:              "https://github.com/example/repo/pull/100",
		PRState:            "merged",
		MergedAt:           &mergedAt,
		MergeDurationHours: &mergeDuration,
	}
	issueID1, err := store.InsertIssue(issue1)
	if err != nil {
		return fmt.Errorf("failed to insert issue 1: %w", err)
	}

	issue2 := &db.Issue{
		JobRunID: jobRunID1,
		JiraKey:  "TEST-101",
		JiraURL:  "https://jira.example.com/browse/TEST-101",
		PRNumber: 101,
		PRURL:    "https://github.com/example/repo/pull/101",
		PRState:  "open",
	}
	issueID2, err := store.InsertIssue(issue2)
	if err != nil {
		return fmt.Errorf("failed to insert issue 2: %w", err)
	}

	issue3 := &db.Issue{
		JobRunID:           jobRunID2,
		JiraKey:            "TEST-102",
		JiraURL:            "https://jira.example.com/browse/TEST-102",
		PRNumber:           102,
		PRURL:              "https://github.com/example/repo/pull/102",
		PRState:            "merged",
		MergedAt:           &mergedAt,
		MergeDurationHours: &mergeDuration,
	}
	issueID3, err := store.InsertIssue(issue3)
	if err != nil {
		return fmt.Errorf("failed to insert issue 3: %w", err)
	}

	// Add phase metrics for each issue (4 phases: solve, review, fix, pr)
	phases := []string{"solve", "review", "fix", "pr"}
	for _, issueID := range []int64{issueID1, issueID2, issueID3} {
		for i, phase := range phases {
			metric := &db.PhaseMetric{
				IssueID:             issueID,
				Phase:               phase,
				Status:              "success",
				DurationMs:          int64(5000 + i*1000),
				InputTokens:         int64(1000 + i*100),
				OutputTokens:        int64(500 + i*50),
				CacheReadTokens:     int64(200 + i*20),
				CacheCreationTokens: int64(100 + i*10),
				CostUSD:             0.05 + float64(i)*0.01,
				Model:               "claude-sonnet-4",
				TurnCount:           3 + i,
			}
			if _, err := store.InsertPhaseMetric(metric); err != nil {
				return fmt.Errorf("failed to insert phase metric for issue %d, phase %s: %w", issueID, phase, err)
			}
		}
	}

	// Add 2 review comments for issue 1
	comment1 := &db.ReviewComment{
		IssueID:         issueID1,
		GitHubCommentID: 1001,
		Author:          "reviewer1",
		Body:            "Please add a test for this function",
		CreatedAt:       time.Date(2024, 3, 1, 15, 0, 0, 0, time.UTC),
		Severity:        "suggestion",
		Topic:           "test_gap",
		AIClassified:    true,
		HumanOverride:   false,
	}
	if _, err := store.InsertReviewComment(comment1); err != nil {
		return fmt.Errorf("failed to insert comment 1: %w", err)
	}

	comment2 := &db.ReviewComment{
		IssueID:         issueID1,
		GitHubCommentID: 1002,
		Author:          "reviewer2",
		Body:            "Consider using a more descriptive variable name",
		CreatedAt:       time.Date(2024, 3, 1, 16, 0, 0, 0, time.UTC),
		Severity:        "",
		Topic:           "",
		AIClassified:    false,
		HumanOverride:   false,
	}
	if _, err := store.InsertReviewComment(comment2); err != nil {
		return fmt.Errorf("failed to insert comment 2: %w", err)
	}

	// Add PR complexity for issue 1
	complexity := &db.PRComplexity{
		IssueID:                   issueID1,
		LinesAdded:                50,
		LinesDeleted:              10,
		FilesChanged:              3,
		CyclomaticComplexityDelta: 2.5,
		CognitiveComplexityDelta:  1.5,
	}
	if err := store.InsertOrUpdatePRComplexity(complexity); err != nil {
		return fmt.Errorf("failed to insert PR complexity: %w", err)
	}

	return nil
}
