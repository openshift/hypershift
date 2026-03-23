package scraper

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/openshift/jira-agent-dashboard/internal/db"

	_ "github.com/mattn/go-sqlite3"
)

// --- Mock implementations ---

type mockGCSClient struct {
	builds map[string]map[string][]byte // buildID -> filename -> content
}

func (m *mockGCSClient) ListBuilds(ctx context.Context) ([]string, error) {
	var ids []string
	for id := range m.builds {
		ids = append(ids, id)
	}
	return ids, nil
}

func (m *mockGCSClient) ReadFile(ctx context.Context, buildID, path string) ([]byte, error) {
	files, ok := m.builds[buildID]
	if !ok {
		return nil, fmt.Errorf("build %s not found", buildID)
	}
	data, ok := files[path]
	if !ok {
		return nil, fmt.Errorf("file %s not found in build %s", path, buildID)
	}
	return data, nil
}

type mockGitHubAPI struct {
	prs      map[int]*PRInfo
	comments map[int][]CommentInfo
}

func (m *mockGitHubAPI) GetPR(ctx context.Context, owner, repo string, number int) (*PRInfo, error) {
	pr, ok := m.prs[number]
	if !ok {
		return nil, fmt.Errorf("PR %d not found", number)
	}
	return pr, nil
}

func (m *mockGitHubAPI) GetPRReviewComments(ctx context.Context, owner, repo string, number int) ([]CommentInfo, error) {
	comments, ok := m.comments[number]
	if !ok {
		return nil, nil
	}
	return comments, nil
}

type mockCommentClassifier struct {
	result *Classification
}

func (m *mockCommentClassifier) Classify(ctx context.Context, commentBody string) (*Classification, error) {
	return m.result, nil
}

type mockComplexityAnalyzer struct {
	result *ComplexityResult
}

func (m *mockComplexityAnalyzer) AnalyzePR(ctx context.Context, owner, repo string, prNumber int, baseBranch string) (*ComplexityResult, error) {
	return m.result, nil
}

// --- Helpers ---

func setupTestDB(t *testing.T) *db.Store {
	t.Helper()
	sqlDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return db.NewStore(sqlDB)
}

// --- Tests ---

func TestScrapeNewJobRuns(t *testing.T) {
	store := setupTestDB(t)

	tokensJSON := `{"total_cost_usd":0.05,"duration_ms":12000,"num_turns":3,"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":100,"cache_creation_input_tokens":50,"model":"claude-sonnet-4-20250514"}`
	durationTxt := "120"

	gcs := &mockGCSClient{
		builds: map[string]map[string][]byte{
			"1234567890": {
				"processed-issues.txt":                      []byte("OCPBUGS-123 2024-01-01T00:00:00Z https://github.com/openshift/hypershift/pull/100 pr_created\n"),
				"claude-OCPBUGS-123-solve-tokens.json":      []byte(tokensJSON),
				"claude-OCPBUGS-123-solve-duration.txt":     []byte(durationTxt),
				"claude-OCPBUGS-123-review-tokens.json":     []byte(tokensJSON),
				"claude-OCPBUGS-123-review-duration.txt":    []byte(durationTxt),
				"claude-OCPBUGS-123-fix-tokens.json":        []byte(tokensJSON),
				"claude-OCPBUGS-123-fix-duration.txt":       []byte(durationTxt),
				"claude-OCPBUGS-123-pr-tokens.json":         []byte(tokensJSON),
				"claude-OCPBUGS-123-pr-duration.txt":        []byte(durationTxt),
			},
		},
	}

	gh := &mockGitHubAPI{prs: map[int]*PRInfo{}, comments: map[int][]CommentInfo{}}
	cl := &mockCommentClassifier{result: &Classification{Severity: "suggestion", Topic: "style"}}
	ca := &mockComplexityAnalyzer{result: &ComplexityResult{CyclomaticDelta: 1.0, CognitiveDelta: 2.0}}

	orch := NewOrchestrator(store, gcs, gh, ca, cl)
	err := orch.scrapeNewJobRuns(context.Background())
	if err != nil {
		t.Fatalf("scrapeNewJobRuns: %v", err)
	}

	// Verify job_run was inserted
	run, err := store.GetJobRunByBuildID("1234567890")
	if err != nil {
		t.Fatalf("GetJobRunByBuildID: %v", err)
	}
	if run.BuildID != "1234567890" {
		t.Errorf("expected build_id 1234567890, got %s", run.BuildID)
	}

	// Verify issue was inserted
	issue, err := store.GetIssueByJiraKey("OCPBUGS-123")
	if err != nil {
		t.Fatalf("GetIssueByJiraKey: %v", err)
	}
	if issue.JiraKey != "OCPBUGS-123" {
		t.Errorf("expected jira_key OCPBUGS-123, got %s", issue.JiraKey)
	}
	if issue.PRNumber != 100 {
		t.Errorf("expected pr_number 100, got %d", issue.PRNumber)
	}
	if issue.PRURL != "https://github.com/openshift/hypershift/pull/100" {
		t.Errorf("expected pr_url, got %s", issue.PRURL)
	}

	// Verify phase metrics were inserted (4 phases)
	metrics, err := store.GetPhaseMetricsByIssueID(issue.ID)
	if err != nil {
		t.Fatalf("GetPhaseMetricsByIssueID: %v", err)
	}
	if len(metrics) != 4 {
		t.Errorf("expected 4 phase metrics, got %d", len(metrics))
	}
}

func TestRefreshOpenPRs(t *testing.T) {
	store := setupTestDB(t)

	// Insert a job run and an open issue with a PR
	runID, _ := store.InsertJobRun(&db.JobRun{
		ProwJobID:   "pj-1",
		BuildID:     "build-1",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		Status:      "success",
		ArtifactURL: "http://example.com",
	})
	_, _ = store.InsertIssue(&db.Issue{
		JobRunID: runID,
		JiraKey:  "OCPBUGS-200",
		JiraURL:  "https://issues.redhat.com/browse/OCPBUGS-200",
		PRNumber: 200,
		PRURL:    "https://github.com/openshift/hypershift/pull/200",
		PRState:  "open",
	})

	mergedAt := time.Now()
	gh := &mockGitHubAPI{
		prs: map[int]*PRInfo{
			200: {
				State:        "closed",
				Merged:       true,
				MergedAt:     &mergedAt,
				Additions:    10,
				Deletions:    5,
				ChangedFiles: 3,
			},
		},
		comments: map[int][]CommentInfo{
			200: {
				{
					ID:        9001,
					Author:    "reviewer1",
					Body:      "This looks wrong",
					CreatedAt: time.Now(),
				},
			},
		},
	}

	cl := &mockCommentClassifier{result: &Classification{Severity: "suggestion", Topic: "style"}}
	ca := &mockComplexityAnalyzer{result: &ComplexityResult{CyclomaticDelta: 0.5, CognitiveDelta: 1.5}}
	gcs := &mockGCSClient{builds: map[string]map[string][]byte{}}

	orch := NewOrchestrator(store, gcs, gh, ca, cl)
	err := orch.refreshOpenPRs(context.Background())
	if err != nil {
		t.Fatalf("refreshOpenPRs: %v", err)
	}

	// Verify issue state was updated
	issue, err := store.GetIssueByJiraKey("OCPBUGS-200")
	if err != nil {
		t.Fatalf("GetIssueByJiraKey: %v", err)
	}
	if issue.PRState != "merged" {
		t.Errorf("expected pr_state merged, got %s", issue.PRState)
	}
	if issue.MergedAt == nil {
		t.Error("expected merged_at to be set")
	}

	// Verify review comment was inserted
	comments, err := store.GetReviewCommentsByIssueID(issue.ID)
	if err != nil {
		t.Fatalf("GetReviewCommentsByIssueID: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].GitHubCommentID != 9001 {
		t.Errorf("expected github_comment_id 9001, got %d", comments[0].GitHubCommentID)
	}

	// Verify PR complexity was inserted
	complexity, err := store.GetPRComplexityByIssueID(issue.ID)
	if err != nil {
		t.Fatalf("GetPRComplexityByIssueID: %v", err)
	}
	if complexity.LinesAdded != 10 {
		t.Errorf("expected lines_added 10, got %d", complexity.LinesAdded)
	}
	if complexity.CyclomaticComplexityDelta != 0.5 {
		t.Errorf("expected cyclomatic_complexity_delta 0.5, got %f", complexity.CyclomaticComplexityDelta)
	}
}

func TestSkipAlreadyScrapedBuilds(t *testing.T) {
	store := setupTestDB(t)

	// Pre-insert a job run with this build ID
	_, _ = store.InsertJobRun(&db.JobRun{
		ProwJobID:   "pj-existing",
		BuildID:     "existing-build",
		StartedAt:   time.Now(),
		FinishedAt:  time.Now(),
		Status:      "success",
		ArtifactURL: "http://example.com",
	})

	gcs := &mockGCSClient{
		builds: map[string]map[string][]byte{
			"existing-build": {
				"processed-issues.txt": []byte("OCPBUGS-999 2024-01-01T00:00:00Z https://github.com/openshift/hypershift/pull/999 pr_created\n"),
			},
		},
	}

	gh := &mockGitHubAPI{prs: map[int]*PRInfo{}, comments: map[int][]CommentInfo{}}
	cl := &mockCommentClassifier{result: &Classification{Severity: "suggestion", Topic: "style"}}
	ca := &mockComplexityAnalyzer{result: &ComplexityResult{}}

	orch := NewOrchestrator(store, gcs, gh, ca, cl)
	err := orch.scrapeNewJobRuns(context.Background())
	if err != nil {
		t.Fatalf("scrapeNewJobRuns: %v", err)
	}

	// Verify no issue was inserted (the build was already scraped)
	_, err = store.GetIssueByJiraKey("OCPBUGS-999")
	if err == nil {
		t.Error("expected issue OCPBUGS-999 not to be inserted, but it was")
	}
}

func TestStopRefreshingMergedPRs(t *testing.T) {
	store := setupTestDB(t)

	// Insert a job run and an issue that was merged 8 days ago
	runID, _ := store.InsertJobRun(&db.JobRun{
		ProwJobID:   "pj-old",
		BuildID:     "build-old",
		StartedAt:   time.Now().Add(-10 * 24 * time.Hour),
		FinishedAt:  time.Now().Add(-10 * 24 * time.Hour),
		Status:      "success",
		ArtifactURL: "http://example.com",
	})
	mergedAt := time.Now().Add(-8 * 24 * time.Hour)
	mergeDuration := 24.0
	_, _ = store.InsertIssue(&db.Issue{
		JobRunID:           runID,
		JiraKey:            "OCPBUGS-300",
		JiraURL:            "https://issues.redhat.com/browse/OCPBUGS-300",
		PRNumber:           300,
		PRURL:              "https://github.com/openshift/hypershift/pull/300",
		PRState:            "open",
		MergedAt:           &mergedAt,
		MergeDurationHours: &mergeDuration,
	})

	// The GitHub mock should NOT be called; if it is, the PR info will differ
	gh := &mockGitHubAPI{
		prs: map[int]*PRInfo{
			300: {
				State:        "closed",
				Merged:       true,
				MergedAt:     &mergedAt,
				Additions:    999,
				Deletions:    999,
				ChangedFiles: 999,
			},
		},
		comments: map[int][]CommentInfo{},
	}

	cl := &mockCommentClassifier{result: &Classification{Severity: "suggestion", Topic: "style"}}
	ca := &mockComplexityAnalyzer{result: &ComplexityResult{CyclomaticDelta: 99, CognitiveDelta: 99}}
	gcs := &mockGCSClient{builds: map[string]map[string][]byte{}}

	orch := NewOrchestrator(store, gcs, gh, ca, cl)
	err := orch.refreshOpenPRs(context.Background())
	if err != nil {
		t.Fatalf("refreshOpenPRs: %v", err)
	}

	// The issue state should remain "open" because the merged_at is > 7 days ago,
	// so we skip refreshing it. PR complexity should NOT be inserted.
	_, err = store.GetPRComplexityByIssueID(1)
	if err == nil {
		t.Error("expected no PR complexity to be inserted for old merged PR, but it was")
	}
}
