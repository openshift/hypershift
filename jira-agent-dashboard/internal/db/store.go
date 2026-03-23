package db

import (
	"database/sql"
	"time"
)

// Store wraps a sql.DB connection and provides methods for CRUD operations.
type Store struct {
	db *sql.DB
}

// NewStore creates a new Store with the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// InsertJobRun inserts a new job run and returns its ID.
func (s *Store) InsertJobRun(run *JobRun) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO job_runs (prow_job_id, build_id, started_at, finished_at, status, artifact_url)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		run.ProwJobID, run.BuildID, run.StartedAt, run.FinishedAt, run.Status, run.ArtifactURL,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetJobRunByBuildID retrieves a job run by its build ID.
func (s *Store) GetJobRunByBuildID(buildID string) (*JobRun, error) {
	row := s.db.QueryRow(
		`SELECT id, prow_job_id, build_id, started_at, finished_at, status, artifact_url
		 FROM job_runs WHERE build_id = ?`, buildID,
	)
	var run JobRun
	err := row.Scan(&run.ID, &run.ProwJobID, &run.BuildID, &run.StartedAt, &run.FinishedAt, &run.Status, &run.ArtifactURL)
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// InsertIssue inserts a new issue and returns its ID.
func (s *Store) InsertIssue(issue *Issue) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO issues (job_run_id, jira_key, jira_url, pr_number, pr_url, pr_state, merged_at, merge_duration_hours)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.JobRunID, issue.JiraKey, issue.JiraURL, issue.PRNumber, issue.PRURL, issue.PRState,
		issue.MergedAt, issue.MergeDurationHours,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetIssueByJiraKey retrieves an issue by its Jira key.
func (s *Store) GetIssueByJiraKey(jiraKey string) (*Issue, error) {
	return s.scanIssue(s.db.QueryRow(
		`SELECT id, job_run_id, jira_key, jira_url, pr_number, pr_url, pr_state, merged_at, merge_duration_hours
		 FROM issues WHERE jira_key = ?`, jiraKey,
	))
}

// GetIssueByID retrieves an issue by its ID.
func (s *Store) GetIssueByID(id int64) (*Issue, error) {
	return s.scanIssue(s.db.QueryRow(
		`SELECT id, job_run_id, jira_key, jira_url, pr_number, pr_url, pr_state, merged_at, merge_duration_hours
		 FROM issues WHERE id = ?`, id,
	))
}

func (s *Store) scanIssue(row *sql.Row) (*Issue, error) {
	var issue Issue
	var prURL, prState sql.NullString
	var prNumber sql.NullInt64
	err := row.Scan(
		&issue.ID, &issue.JobRunID, &issue.JiraKey, &issue.JiraURL,
		&prNumber, &prURL, &prState,
		&issue.MergedAt, &issue.MergeDurationHours,
	)
	if err != nil {
		return nil, err
	}
	if prNumber.Valid {
		issue.PRNumber = int(prNumber.Int64)
	}
	if prURL.Valid {
		issue.PRURL = prURL.String
	}
	if prState.Valid {
		issue.PRState = prState.String
	}
	return &issue, nil
}

// UpdateIssueState updates the PR state, merged_at, and merge_duration_hours for an issue.
func (s *Store) UpdateIssueState(id int64, prState string, mergedAt *time.Time, mergeDurationHours *float64) error {
	_, err := s.db.Exec(
		`UPDATE issues SET pr_state = ?, merged_at = ?, merge_duration_hours = ? WHERE id = ?`,
		prState, mergedAt, mergeDurationHours, id,
	)
	return err
}

// ListIssues returns issues where the associated job_run started_at is within the given range.
func (s *Store) ListIssues(from, to time.Time) ([]Issue, error) {
	rows, err := s.db.Query(
		`SELECT i.id, i.job_run_id, i.jira_key, i.jira_url, i.pr_number, i.pr_url, i.pr_state, i.merged_at, i.merge_duration_hours
		 FROM issues i
		 JOIN job_runs jr ON i.job_run_id = jr.id
		 WHERE jr.started_at >= ? AND jr.started_at < ?`,
		from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanIssues(rows)
}

func (s *Store) scanIssues(rows *sql.Rows) ([]Issue, error) {
	var issues []Issue
	for rows.Next() {
		var issue Issue
		var prURL, prState sql.NullString
		var prNumber sql.NullInt64
		err := rows.Scan(
			&issue.ID, &issue.JobRunID, &issue.JiraKey, &issue.JiraURL,
			&prNumber, &prURL, &prState,
			&issue.MergedAt, &issue.MergeDurationHours,
		)
		if err != nil {
			return nil, err
		}
		if prNumber.Valid {
			issue.PRNumber = int(prNumber.Int64)
		}
		if prURL.Valid {
			issue.PRURL = prURL.String
		}
		if prState.Valid {
			issue.PRState = prState.String
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

// InsertPhaseMetric inserts a new phase metric and returns its ID.
func (s *Store) InsertPhaseMetric(m *PhaseMetric) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO phase_metrics (issue_id, phase, status, duration_ms, input_tokens, output_tokens,
		 cache_read_tokens, cache_creation_tokens, cost_usd, model, turn_count, error_text)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.IssueID, m.Phase, m.Status, m.DurationMs, m.InputTokens, m.OutputTokens,
		m.CacheReadTokens, m.CacheCreationTokens, m.CostUSD, m.Model, m.TurnCount, m.ErrorText,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetPhaseMetricsByIssueID retrieves all phase metrics for a given issue.
func (s *Store) GetPhaseMetricsByIssueID(issueID int64) ([]PhaseMetric, error) {
	rows, err := s.db.Query(
		`SELECT id, issue_id, phase, status, duration_ms, input_tokens, output_tokens,
		 cache_read_tokens, cache_creation_tokens, cost_usd, model, turn_count, error_text
		 FROM phase_metrics WHERE issue_id = ? ORDER BY id`, issueID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []PhaseMetric
	for rows.Next() {
		var m PhaseMetric
		err := rows.Scan(
			&m.ID, &m.IssueID, &m.Phase, &m.Status, &m.DurationMs, &m.InputTokens, &m.OutputTokens,
			&m.CacheReadTokens, &m.CacheCreationTokens, &m.CostUSD, &m.Model, &m.TurnCount, &m.ErrorText,
		)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

// InsertReviewComment inserts a new review comment and returns its ID.
func (s *Store) InsertReviewComment(c *ReviewComment) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO review_comments (issue_id, github_comment_id, author, body, created_at, severity, topic, ai_classified, human_override)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.IssueID, c.GitHubCommentID, c.Author, c.Body, c.CreatedAt,
		nilIfEmpty(c.Severity), nilIfEmpty(c.Topic),
		boolToInt(c.AIClassified), boolToInt(c.HumanOverride),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetReviewCommentsByIssueID retrieves all review comments for a given issue.
func (s *Store) GetReviewCommentsByIssueID(issueID int64) ([]ReviewComment, error) {
	rows, err := s.db.Query(
		`SELECT id, issue_id, github_comment_id, author, body, created_at, severity, topic, ai_classified, human_override
		 FROM review_comments WHERE issue_id = ? ORDER BY id`, issueID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanReviewComments(rows)
}

// GetUnclassifiedComments returns comments where severity is NULL.
func (s *Store) GetUnclassifiedComments() ([]ReviewComment, error) {
	rows, err := s.db.Query(
		`SELECT id, issue_id, github_comment_id, author, body, created_at, severity, topic, ai_classified, human_override
		 FROM review_comments WHERE severity IS NULL ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanReviewComments(rows)
}

func (s *Store) scanReviewComments(rows *sql.Rows) ([]ReviewComment, error) {
	var comments []ReviewComment
	for rows.Next() {
		var c ReviewComment
		var severity, topic sql.NullString
		var aiClassified, humanOverride int
		err := rows.Scan(
			&c.ID, &c.IssueID, &c.GitHubCommentID, &c.Author, &c.Body, &c.CreatedAt,
			&severity, &topic, &aiClassified, &humanOverride,
		)
		if err != nil {
			return nil, err
		}
		if severity.Valid {
			c.Severity = severity.String
		}
		if topic.Valid {
			c.Topic = topic.String
		}
		c.AIClassified = aiClassified != 0
		c.HumanOverride = humanOverride != 0
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// UpdateCommentClassification updates the severity, topic, and human_override for a comment.
func (s *Store) UpdateCommentClassification(id int64, severity, topic string, humanOverride bool) error {
	_, err := s.db.Exec(
		`UPDATE review_comments SET severity = ?, topic = ?, human_override = ? WHERE id = ?`,
		severity, topic, boolToInt(humanOverride), id,
	)
	return err
}

// InsertOrUpdatePRComplexity upserts PR complexity data for an issue.
func (s *Store) InsertOrUpdatePRComplexity(c *PRComplexity) error {
	_, err := s.db.Exec(
		`INSERT INTO pr_complexity (issue_id, lines_added, lines_deleted, files_changed, cyclomatic_complexity_delta, cognitive_complexity_delta)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(issue_id) DO UPDATE SET
		   lines_added = excluded.lines_added,
		   lines_deleted = excluded.lines_deleted,
		   files_changed = excluded.files_changed,
		   cyclomatic_complexity_delta = excluded.cyclomatic_complexity_delta,
		   cognitive_complexity_delta = excluded.cognitive_complexity_delta`,
		c.IssueID, c.LinesAdded, c.LinesDeleted, c.FilesChanged,
		c.CyclomaticComplexityDelta, c.CognitiveComplexityDelta,
	)
	return err
}

// GetPRComplexityByIssueID retrieves PR complexity data for a given issue.
func (s *Store) GetPRComplexityByIssueID(issueID int64) (*PRComplexity, error) {
	row := s.db.QueryRow(
		`SELECT id, issue_id, lines_added, lines_deleted, files_changed, cyclomatic_complexity_delta, cognitive_complexity_delta
		 FROM pr_complexity WHERE issue_id = ?`, issueID,
	)
	var c PRComplexity
	err := row.Scan(&c.ID, &c.IssueID, &c.LinesAdded, &c.LinesDeleted, &c.FilesChanged,
		&c.CyclomaticComplexityDelta, &c.CognitiveComplexityDelta)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetOpenIssues returns issues where pr_state is not "merged" and not "closed".
func (s *Store) GetOpenIssues() ([]Issue, error) {
	rows, err := s.db.Query(
		`SELECT id, job_run_id, jira_key, jira_url, pr_number, pr_url, pr_state, merged_at, merge_duration_hours
		 FROM issues WHERE pr_state IS NOT NULL AND pr_state != 'merged' AND pr_state != 'closed'`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanIssues(rows)
}

// GetWeeklyTrends returns aggregated weekly statistics for issues within the given time range.
// Quality score formula: review_comment_count / (max(lines_changed, 1) * max(files_changed, 1) * max(complexity_delta, 1))
func (s *Store) GetWeeklyTrends(from, to time.Time) ([]WeeklyTrend, error) {
	query := `
WITH issue_stats AS (
	SELECT
		i.id AS issue_id,
		DATE(jr.started_at, 'weekday 0', '-6 days') AS week_start,
		CASE WHEN i.pr_state = 'merged' THEN 1 ELSE 0 END AS is_merged,
		COALESCE((SELECT SUM(pm.cost_usd) FROM phase_metrics pm WHERE pm.issue_id = i.id), 0) AS total_cost,
		COALESCE((SELECT SUM(pm.duration_ms) FROM phase_metrics pm WHERE pm.issue_id = i.id), 0) AS total_duration,
		COALESCE((SELECT COUNT(*) FROM review_comments rc WHERE rc.issue_id = i.id), 0) AS comment_count,
		COALESCE((SELECT pc.lines_added + pc.lines_deleted FROM pr_complexity pc WHERE pc.issue_id = i.id), 0) AS lines_changed,
		COALESCE((SELECT pc.files_changed FROM pr_complexity pc WHERE pc.issue_id = i.id), 0) AS files_changed,
		COALESCE((SELECT pc.cyclomatic_complexity_delta FROM pr_complexity pc WHERE pc.issue_id = i.id), 0) AS complexity_delta
	FROM issues i
	JOIN job_runs jr ON i.job_run_id = jr.id
	WHERE jr.started_at >= ? AND jr.started_at < ?
)
SELECT
	week_start,
	COUNT(*) AS issues_processed,
	CAST(SUM(is_merged) AS REAL) / MAX(COUNT(*), 1) AS merge_rate,
	AVG(total_cost) AS avg_cost_usd,
	AVG(total_duration) AS avg_duration_ms,
	AVG(comment_count) AS avg_review_comments,
	AVG(
		CAST(comment_count AS REAL) / (
			MAX(lines_changed, 1) * MAX(files_changed, 1) * MAX(complexity_delta, 1)
		)
	) AS avg_quality_score
FROM issue_stats
GROUP BY week_start
ORDER BY week_start
`
	rows, err := s.db.Query(query, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trends []WeeklyTrend
	for rows.Next() {
		var tr WeeklyTrend
		var weekStart string
		err := rows.Scan(
			&weekStart, &tr.IssuesProcessed, &tr.MergeRate,
			&tr.AvgCostUSD, &tr.AvgDurationMs, &tr.AvgReviewComments, &tr.AvgQualityScore,
		)
		if err != nil {
			return nil, err
		}
		tr.WeekStart, err = time.Parse("2006-01-02", weekStart)
		if err != nil {
			return nil, err
		}
		trends = append(trends, tr)
	}
	return trends, rows.Err()
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
