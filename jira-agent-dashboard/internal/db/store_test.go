package db

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := InitSchema(conn); err != nil {
		t.Fatal(err)
	}
	return NewStore(conn)
}

func insertTestJobRun(t *testing.T, s *Store, buildID string, startedAt time.Time) int64 {
	t.Helper()
	run := &JobRun{
		ProwJobID:   "prow-123",
		BuildID:     buildID,
		StartedAt:   startedAt,
		FinishedAt:  startedAt.Add(30 * time.Minute),
		Status:      "success",
		ArtifactURL: "https://artifacts.example.com/" + buildID,
	}
	id, err := s.InsertJobRun(run)
	if err != nil {
		t.Fatalf("InsertJobRun failed: %v", err)
	}
	return id
}

func insertTestIssue(t *testing.T, s *Store, jobRunID int64, jiraKey string) int64 {
	t.Helper()
	issue := &Issue{
		JobRunID: jobRunID,
		JiraKey:  jiraKey,
		JiraURL:  "https://issues.redhat.com/browse/" + jiraKey,
		PRNumber: 42,
		PRURL:    "https://github.com/org/repo/pull/42",
		PRState:  "open",
	}
	id, err := s.InsertIssue(issue)
	if err != nil {
		t.Fatalf("InsertIssue failed: %v", err)
	}
	return id
}

func TestInsertAndGetJobRun(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Truncate(time.Second)

	run := &JobRun{
		ProwJobID:   "prow-abc",
		BuildID:     "build-001",
		StartedAt:   now,
		FinishedAt:  now.Add(10 * time.Minute),
		Status:      "success",
		ArtifactURL: "https://artifacts.example.com/build-001",
	}

	id, err := s.InsertJobRun(run)
	if err != nil {
		t.Fatalf("InsertJobRun failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := s.GetJobRunByBuildID("build-001")
	if err != nil {
		t.Fatalf("GetJobRunByBuildID failed: %v", err)
	}
	if got.ProwJobID != "prow-abc" {
		t.Errorf("ProwJobID = %q, want %q", got.ProwJobID, "prow-abc")
	}
	if got.Status != "success" {
		t.Errorf("Status = %q, want %q", got.Status, "success")
	}
	if got.ID != id {
		t.Errorf("ID = %d, want %d", got.ID, id)
	}
}

func TestInsertAndGetIssue(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Truncate(time.Second)
	jobRunID := insertTestJobRun(t, s, "build-100", now)

	issue := &Issue{
		JobRunID: jobRunID,
		JiraKey:  "OCPBUGS-1234",
		JiraURL:  "https://issues.redhat.com/browse/OCPBUGS-1234",
		PRNumber: 99,
		PRURL:    "https://github.com/org/repo/pull/99",
		PRState:  "open",
	}

	id, err := s.InsertIssue(issue)
	if err != nil {
		t.Fatalf("InsertIssue failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := s.GetIssueByJiraKey("OCPBUGS-1234")
	if err != nil {
		t.Fatalf("GetIssueByJiraKey failed: %v", err)
	}
	if got.JobRunID != jobRunID {
		t.Errorf("JobRunID = %d, want %d", got.JobRunID, jobRunID)
	}
	if got.PRNumber != 99 {
		t.Errorf("PRNumber = %d, want 99", got.PRNumber)
	}

	got2, err := s.GetIssueByID(id)
	if err != nil {
		t.Fatalf("GetIssueByID failed: %v", err)
	}
	if got2.JiraKey != "OCPBUGS-1234" {
		t.Errorf("JiraKey = %q, want %q", got2.JiraKey, "OCPBUGS-1234")
	}
}

func TestInsertAndGetPhaseMetrics(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Truncate(time.Second)
	jobRunID := insertTestJobRun(t, s, "build-200", now)
	issueID := insertTestIssue(t, s, jobRunID, "OCPBUGS-2000")

	errText := "context deadline exceeded"
	metrics := []*PhaseMetric{
		{
			IssueID:             issueID,
			Phase:               "solve",
			Status:              "success",
			DurationMs:          15000,
			InputTokens:         1000,
			OutputTokens:        500,
			CacheReadTokens:     200,
			CacheCreationTokens: 100,
			CostUSD:             0.05,
			Model:               "claude-sonnet-4-20250514",
			TurnCount:           3,
		},
		{
			IssueID:    issueID,
			Phase:      "review",
			Status:     "failure",
			DurationMs: 5000,
			CostUSD:    0.02,
			Model:      "claude-sonnet-4-20250514",
			TurnCount:  1,
			ErrorText:  &errText,
		},
	}

	for _, m := range metrics {
		_, err := s.InsertPhaseMetric(m)
		if err != nil {
			t.Fatalf("InsertPhaseMetric failed: %v", err)
		}
	}

	got, err := s.GetPhaseMetricsByIssueID(issueID)
	if err != nil {
		t.Fatalf("GetPhaseMetricsByIssueID failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d metrics, want 2", len(got))
	}
	if got[0].Phase != "solve" {
		t.Errorf("Phase = %q, want %q", got[0].Phase, "solve")
	}
	if got[1].ErrorText == nil || *got[1].ErrorText != errText {
		t.Errorf("ErrorText = %v, want %q", got[1].ErrorText, errText)
	}
}

func TestInsertAndGetReviewComment(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Truncate(time.Second)
	jobRunID := insertTestJobRun(t, s, "build-300", now)
	issueID := insertTestIssue(t, s, jobRunID, "OCPBUGS-3000")

	comment := &ReviewComment{
		IssueID:         issueID,
		GitHubCommentID: 999888,
		Author:          "reviewer1",
		Body:            "Please add tests for this change.",
		CreatedAt:       now,
	}

	id, err := s.InsertReviewComment(comment)
	if err != nil {
		t.Fatalf("InsertReviewComment failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := s.GetReviewCommentsByIssueID(issueID)
	if err != nil {
		t.Fatalf("GetReviewCommentsByIssueID failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d comments, want 1", len(got))
	}
	if got[0].Author != "reviewer1" {
		t.Errorf("Author = %q, want %q", got[0].Author, "reviewer1")
	}
	if got[0].Severity != "" {
		t.Errorf("Severity = %q, want empty", got[0].Severity)
	}

	// Verify it shows up as unclassified
	unclassified, err := s.GetUnclassifiedComments()
	if err != nil {
		t.Fatalf("GetUnclassifiedComments failed: %v", err)
	}
	if len(unclassified) != 1 {
		t.Fatalf("got %d unclassified, want 1", len(unclassified))
	}
}

func TestInsertAndGetPRComplexity(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Truncate(time.Second)
	jobRunID := insertTestJobRun(t, s, "build-400", now)
	issueID := insertTestIssue(t, s, jobRunID, "OCPBUGS-4000")

	c := &PRComplexity{
		IssueID:                   issueID,
		LinesAdded:                150,
		LinesDeleted:              30,
		FilesChanged:              5,
		CyclomaticComplexityDelta: 3.5,
		CognitiveComplexityDelta:  2.0,
	}

	if err := s.InsertOrUpdatePRComplexity(c); err != nil {
		t.Fatalf("InsertOrUpdatePRComplexity failed: %v", err)
	}

	got, err := s.GetPRComplexityByIssueID(issueID)
	if err != nil {
		t.Fatalf("GetPRComplexityByIssueID failed: %v", err)
	}
	if got.LinesAdded != 150 {
		t.Errorf("LinesAdded = %d, want 150", got.LinesAdded)
	}
	if got.FilesChanged != 5 {
		t.Errorf("FilesChanged = %d, want 5", got.FilesChanged)
	}

	// Test upsert: update the same issue_id
	c.LinesAdded = 200
	if err := s.InsertOrUpdatePRComplexity(c); err != nil {
		t.Fatalf("InsertOrUpdatePRComplexity (upsert) failed: %v", err)
	}

	got2, err := s.GetPRComplexityByIssueID(issueID)
	if err != nil {
		t.Fatalf("GetPRComplexityByIssueID after upsert failed: %v", err)
	}
	if got2.LinesAdded != 200 {
		t.Errorf("LinesAdded after upsert = %d, want 200", got2.LinesAdded)
	}
}

func TestListIssuesWithDateRange(t *testing.T) {
	s := newTestStore(t)

	// Create job runs on different dates
	day1 := time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	day3 := time.Date(2025, 1, 20, 12, 0, 0, 0, time.UTC)

	jr1 := insertTestJobRun(t, s, "build-d1", day1)
	jr2 := insertTestJobRun(t, s, "build-d2", day2)
	jr3 := insertTestJobRun(t, s, "build-d3", day3)

	insertTestIssue(t, s, jr1, "OCPBUGS-D1")
	insertTestIssue(t, s, jr2, "OCPBUGS-D2")
	insertTestIssue(t, s, jr3, "OCPBUGS-D3")

	// Query range that includes day1 and day2 but not day3
	from := time.Date(2025, 1, 9, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC)

	issues, err := s.ListIssues(from, to)
	if err != nil {
		t.Fatalf("ListIssues failed: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
}

func TestUpdateCommentClassification(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Truncate(time.Second)
	jobRunID := insertTestJobRun(t, s, "build-500", now)
	issueID := insertTestIssue(t, s, jobRunID, "OCPBUGS-5000")

	comment := &ReviewComment{
		IssueID:         issueID,
		GitHubCommentID: 777666,
		Author:          "bot",
		Body:            "Nit: variable naming",
		CreatedAt:       now,
	}

	id, err := s.InsertReviewComment(comment)
	if err != nil {
		t.Fatalf("InsertReviewComment failed: %v", err)
	}

	err = s.UpdateCommentClassification(id, "nitpick", "style", true)
	if err != nil {
		t.Fatalf("UpdateCommentClassification failed: %v", err)
	}

	got, err := s.GetReviewCommentsByIssueID(issueID)
	if err != nil {
		t.Fatalf("GetReviewCommentsByIssueID failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d comments, want 1", len(got))
	}
	if got[0].Severity != "nitpick" {
		t.Errorf("Severity = %q, want %q", got[0].Severity, "nitpick")
	}
	if got[0].Topic != "style" {
		t.Errorf("Topic = %q, want %q", got[0].Topic, "style")
	}
	if !got[0].HumanOverride {
		t.Error("HumanOverride = false, want true")
	}

	// After classification, it should not appear in unclassified list
	unclassified, err := s.GetUnclassifiedComments()
	if err != nil {
		t.Fatalf("GetUnclassifiedComments failed: %v", err)
	}
	if len(unclassified) != 0 {
		t.Errorf("got %d unclassified, want 0", len(unclassified))
	}
}

func TestGetOpenIssues(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Truncate(time.Second)
	jobRunID := insertTestJobRun(t, s, "build-600", now)

	// Insert open, merged, and closed issues
	insertTestIssue(t, s, jobRunID, "OCPBUGS-OPEN1") // pr_state = "open"

	mergedAt := now.Add(-1 * time.Hour)
	mergeDuration := 2.5
	issue2 := &Issue{
		JobRunID: jobRunID,
		JiraKey:  "OCPBUGS-MERGED1",
		JiraURL:  "https://issues.redhat.com/browse/OCPBUGS-MERGED1",
		PRNumber: 50,
		PRURL:    "https://github.com/org/repo/pull/50",
		PRState:  "merged",
		MergedAt: &mergedAt,
		MergeDurationHours: &mergeDuration,
	}
	if _, err := s.InsertIssue(issue2); err != nil {
		t.Fatal(err)
	}

	issue3 := &Issue{
		JobRunID: jobRunID,
		JiraKey:  "OCPBUGS-CLOSED1",
		JiraURL:  "https://issues.redhat.com/browse/OCPBUGS-CLOSED1",
		PRState:  "closed",
	}
	if _, err := s.InsertIssue(issue3); err != nil {
		t.Fatal(err)
	}

	open, err := s.GetOpenIssues()
	if err != nil {
		t.Fatalf("GetOpenIssues failed: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("got %d open issues, want 1", len(open))
	}
	if open[0].JiraKey != "OCPBUGS-OPEN1" {
		t.Errorf("JiraKey = %q, want %q", open[0].JiraKey, "OCPBUGS-OPEN1")
	}
}

func TestUpdateIssueState(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().Truncate(time.Second)
	jobRunID := insertTestJobRun(t, s, "build-700", now)
	issueID := insertTestIssue(t, s, jobRunID, "OCPBUGS-7000")

	mergedAt := now
	duration := 1.5
	err := s.UpdateIssueState(issueID, "merged", &mergedAt, &duration)
	if err != nil {
		t.Fatalf("UpdateIssueState failed: %v", err)
	}

	got, err := s.GetIssueByID(issueID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PRState != "merged" {
		t.Errorf("PRState = %q, want %q", got.PRState, "merged")
	}
	if got.MergeDurationHours == nil || *got.MergeDurationHours != 1.5 {
		t.Errorf("MergeDurationHours = %v, want 1.5", got.MergeDurationHours)
	}
}

func TestGetWeeklyTrends(t *testing.T) {
	s := newTestStore(t)

	// Create data for two weeks
	week1Start := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC) // Monday
	week2Start := time.Date(2025, 1, 13, 10, 0, 0, 0, time.UTC)

	jr1 := insertTestJobRun(t, s, "build-w1", week1Start)
	jr2 := insertTestJobRun(t, s, "build-w2", week2Start)

	issue1ID := insertTestIssue(t, s, jr1, "OCPBUGS-W1")
	issue2ID := insertTestIssue(t, s, jr2, "OCPBUGS-W2")

	// Mark issue2 as merged
	mergedAt := week2Start.Add(2 * time.Hour)
	duration := 2.0
	if err := s.UpdateIssueState(issue2ID, "merged", &mergedAt, &duration); err != nil {
		t.Fatal(err)
	}

	// Add phase metrics
	for _, issueID := range []int64{issue1ID, issue2ID} {
		if _, err := s.InsertPhaseMetric(&PhaseMetric{
			IssueID:    issueID,
			Phase:      "solve",
			Status:     "success",
			DurationMs: 10000,
			CostUSD:    0.10,
			Model:      "claude-sonnet-4-20250514",
			TurnCount:  2,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Add review comments
	if _, err := s.InsertReviewComment(&ReviewComment{
		IssueID:         issue1ID,
		GitHubCommentID: 111,
		Author:          "rev",
		Body:            "Fix this",
		CreatedAt:       week1Start,
	}); err != nil {
		t.Fatal(err)
	}

	// Add PR complexity
	if err := s.InsertOrUpdatePRComplexity(&PRComplexity{
		IssueID:                   issue1ID,
		LinesAdded:                100,
		LinesDeleted:              20,
		FilesChanged:              3,
		CyclomaticComplexityDelta: 2.0,
		CognitiveComplexityDelta:  1.0,
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.InsertOrUpdatePRComplexity(&PRComplexity{
		IssueID:                   issue2ID,
		LinesAdded:                50,
		LinesDeleted:              10,
		FilesChanged:              2,
		CyclomaticComplexityDelta: 1.0,
		CognitiveComplexityDelta:  0.5,
	}); err != nil {
		t.Fatal(err)
	}

	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC)

	trends, err := s.GetWeeklyTrends(from, to)
	if err != nil {
		t.Fatalf("GetWeeklyTrends failed: %v", err)
	}
	if len(trends) == 0 {
		t.Fatal("expected at least one weekly trend")
	}

	// Verify basic structure
	for _, tr := range trends {
		if tr.IssuesProcessed == 0 {
			t.Error("IssuesProcessed should be > 0")
		}
	}
}
