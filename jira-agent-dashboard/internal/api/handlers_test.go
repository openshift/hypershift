package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/openshift/jira-agent-dashboard/internal/db"

	_ "github.com/mattn/go-sqlite3"
)

func newTestServer(t *testing.T) (*httptest.Server, *db.Store) {
	t.Helper()
	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := db.InitSchema(conn); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(conn)
	srv := NewServer(store, t.TempDir())
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, store
}

func seedTestData(t *testing.T, store *db.Store) (issueID int64, commentID int64) {
	t.Helper()
	started := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	jobRunID, err := store.InsertJobRun(&db.JobRun{
		ProwJobID:   "prow-1",
		BuildID:     "build-1",
		StartedAt:   started,
		FinishedAt:  started.Add(30 * time.Minute),
		Status:      "success",
		ArtifactURL: "https://artifacts.example.com/build-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	mergedAt := started.Add(24 * time.Hour)
	mergeDuration := 24.0
	issueID, err = store.InsertIssue(&db.Issue{
		JobRunID:           jobRunID,
		JiraKey:            "TEST-123",
		JiraURL:            "https://issues.redhat.com/browse/TEST-123",
		PRNumber:           42,
		PRURL:              "https://github.com/org/repo/pull/42",
		PRState:            "merged",
		MergedAt:           &mergedAt,
		MergeDurationHours: &mergeDuration,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertPhaseMetric(&db.PhaseMetric{
		IssueID:             issueID,
		Phase:               "solve",
		Status:              "success",
		DurationMs:          120000,
		InputTokens:         5000,
		OutputTokens:        2000,
		CacheReadTokens:     1000,
		CacheCreationTokens: 500,
		CostUSD:             0.15,
		Model:               "claude-sonnet-4-20250514",
		TurnCount:           10,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.InsertPhaseMetric(&db.PhaseMetric{
		IssueID:             issueID,
		Phase:               "review",
		Status:              "success",
		DurationMs:          60000,
		InputTokens:         3000,
		OutputTokens:        1000,
		CacheReadTokens:     500,
		CacheCreationTokens: 200,
		CostUSD:             0.08,
		Model:               "claude-sonnet-4-20250514",
		TurnCount:           5,
	})
	if err != nil {
		t.Fatal(err)
	}

	commentID, err = store.InsertReviewComment(&db.ReviewComment{
		IssueID:         issueID,
		GitHubCommentID: 1001,
		Author:          "reviewer1",
		Body:            "Consider using a constant here.",
		CreatedAt:       started.Add(2 * time.Hour),
		Severity:        "suggestion",
		Topic:           "style",
		AIClassified:    true,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = store.InsertOrUpdatePRComplexity(&db.PRComplexity{
		IssueID:                   issueID,
		LinesAdded:                50,
		LinesDeleted:              10,
		FilesChanged:              3,
		CyclomaticComplexityDelta: 2.0,
		CognitiveComplexityDelta:  1.5,
	})
	if err != nil {
		t.Fatal(err)
	}

	return issueID, commentID
}

func TestGetTrends(t *testing.T) {
	ts, store := newTestServer(t)
	seedTestData(t, store)

	resp, err := http.Get(ts.URL + "/api/trends?from=2026-01-01&to=2026-03-01")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var trends []TrendPoint
	if err := json.NewDecoder(resp.Body).Decode(&trends); err != nil {
		t.Fatal(err)
	}

	if len(trends) == 0 {
		t.Fatal("expected at least one trend point")
	}

	tr := trends[0]
	if tr.WeekStart == "" {
		t.Error("expected week_start to be set")
	}
	if tr.IssuesProcessed != 1 {
		t.Errorf("expected issues_processed=1, got %d", tr.IssuesProcessed)
	}
	if tr.MergeRate != 1.0 {
		t.Errorf("expected merge_rate=1.0, got %f", tr.MergeRate)
	}
}

func TestGetIssues(t *testing.T) {
	ts, store := newTestServer(t)
	seedTestData(t, store)

	resp, err := http.Get(ts.URL + "/api/issues?from=2026-01-01&to=2026-03-01")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var issues []IssueSummary
	if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
		t.Fatal(err)
	}

	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}

	issue := issues[0]
	if issue.JiraKey != "TEST-123" {
		t.Errorf("expected jira_key=TEST-123, got %s", issue.JiraKey)
	}
	if issue.PRState != "merged" {
		t.Errorf("expected pr_state=merged, got %s", issue.PRState)
	}
	if issue.ReviewComments != 1 {
		t.Errorf("expected review_comments=1, got %d", issue.ReviewComments)
	}
	if issue.LinesChanged != 60 {
		t.Errorf("expected lines_changed=60, got %d", issue.LinesChanged)
	}
	if issue.FilesChanged != 3 {
		t.Errorf("expected files_changed=3, got %d", issue.FilesChanged)
	}
	expectedCost := 0.23
	if diff := issue.CostUSD - expectedCost; diff > 0.001 || diff < -0.001 {
		t.Errorf("expected cost_usd~=%.2f, got %f", expectedCost, issue.CostUSD)
	}
	if issue.DurationMs != 180000 {
		t.Errorf("expected duration_ms=180000, got %d", issue.DurationMs)
	}
}

func TestGetIssueDetail(t *testing.T) {
	ts, store := newTestServer(t)
	issueID, _ := seedTestData(t, store)

	resp, err := http.Get(ts.URL + "/api/issues/" + itoa(issueID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var detail IssueDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}

	if detail.JiraKey != "TEST-123" {
		t.Errorf("expected jira_key=TEST-123, got %s", detail.JiraKey)
	}
	if len(detail.Phases) != 2 {
		t.Errorf("expected 2 phases, got %d", len(detail.Phases))
	}
	if len(detail.Comments) != 1 {
		t.Errorf("expected 1 comment, got %d", len(detail.Comments))
	}
	if detail.ComplexityDelta != 2.0 {
		t.Errorf("expected complexity_delta=2.0, got %f", detail.ComplexityDelta)
	}
}

func TestGetComments(t *testing.T) {
	ts, store := newTestServer(t)
	issueID, _ := seedTestData(t, store)

	resp, err := http.Get(ts.URL + "/api/comments/" + itoa(issueID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var comments []CommentDetail
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		t.Fatal(err)
	}

	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}

	c := comments[0]
	if c.Author != "reviewer1" {
		t.Errorf("expected author=reviewer1, got %s", c.Author)
	}
	if c.Severity != "suggestion" {
		t.Errorf("expected severity=suggestion, got %s", c.Severity)
	}
	if c.Topic != "style" {
		t.Errorf("expected topic=style, got %s", c.Topic)
	}
	if !c.AIClassified {
		t.Error("expected ai_classified=true")
	}
}

func TestPatchComment(t *testing.T) {
	ts, store := newTestServer(t)
	_, commentID := seedTestData(t, store)

	body := `{"severity":"required_change","topic":"logic_bug"}`
	req, err := http.NewRequest("PATCH", ts.URL+"/api/comments/"+itoa(commentID), bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var updated CommentDetail
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}

	if updated.Severity != "required_change" {
		t.Errorf("expected severity=required_change, got %s", updated.Severity)
	}
	if updated.Topic != "logic_bug" {
		t.Errorf("expected topic=logic_bug, got %s", updated.Topic)
	}
	if !updated.HumanOverride {
		t.Error("expected human_override=true")
	}
}

func TestPatchCommentInvalidSeverity(t *testing.T) {
	ts, store := newTestServer(t)
	_, commentID := seedTestData(t, store)

	body := `{"severity":"invalid","topic":"logic_bug"}`
	req, err := http.NewRequest("PATCH", ts.URL+"/api/comments/"+itoa(commentID), bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestPatchCommentInvalidTopic(t *testing.T) {
	ts, store := newTestServer(t)
	_, commentID := seedTestData(t, store)

	body := `{"severity":"suggestion","topic":"invalid"}`
	req, err := http.NewRequest("PATCH", ts.URL+"/api/comments/"+itoa(commentID), bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
