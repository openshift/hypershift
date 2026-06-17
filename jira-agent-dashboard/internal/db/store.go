package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// allowedBots is the list of bot authors whose comments should be included
// in review metrics. All other bot-like authors (ending in [bot], containing
// -robot, or known CI bots) are excluded.
var allowedBots = []string{
	"coderabbitai[bot]",
}

// commentFilterSQL returns a SQL WHERE clause fragment that excludes bot authors
// (except those in the allowedBots list) and slash-command comments (body starts
// with /word). The authorCol and bodyCol parameters are the fully qualified
// column names (e.g., "rc.author", "rc.body").
func commentFilterSQL(authorCol, bodyCol string) string {
	allowed := make([]string, len(allowedBots))
	for i, b := range allowedBots {
		allowed[i] = "'" + b + "'"
	}
	allowedCSV := strings.Join(allowed, ", ")

	// Exclude authors that look like bots UNLESS they're in the allowed list.
	// Bot patterns: ends with [bot], ends with -robot, or is a known CI bot.
	// Also exclude slash-command comments (/lgtm, /test, /approve, /retest, etc.)
	// and automated "no actionable comments" summaries from CodeRabbit.
	return fmt.Sprintf(
		`AND (%s IN (%s) OR (%s NOT LIKE '%%[bot]' AND %s NOT LIKE '%%-robot' AND %s NOT IN ('cwbotbot')))`+
			` AND TRIM(%s, CHAR(10)||CHAR(13)||CHAR(32)||CHAR(9)) NOT GLOB '/[a-z]*'`+
			` AND %s NOT LIKE '%%No actionable comments were generated%%'`+
			` AND %s NOT LIKE '%%Skipped: comment is from another GitHub bot%%'`+
			` AND %s NOT LIKE '%%<!-- walkthrough_start -->%%'`+
			` AND %s NOT LIKE '%%skip review by coderabbit.ai%%'`,
		authorCol, allowedCSV, authorCol, authorCol, authorCol, bodyCol, bodyCol, bodyCol, bodyCol, bodyCol)
}

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

// UpdateJobRunTimestamps updates the started_at and finished_at for a job run.
func (s *Store) UpdateJobRunTimestamps(id int64, startedAt, finishedAt time.Time) error {
	_, err := s.db.Exec(
		`UPDATE job_runs SET started_at = ?, finished_at = ? WHERE id = ?`,
		startedAt, finishedAt, id,
	)
	return err
}

// ListAllJobRuns returns all job runs.
func (s *Store) ListAllJobRuns() ([]JobRun, error) {
	rows, err := s.db.Query(`SELECT id, prow_job_id, build_id, started_at, finished_at, status, artifact_url FROM job_runs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []JobRun
	for rows.Next() {
		var r JobRun
		if err := rows.Scan(&r.ID, &r.ProwJobID, &r.BuildID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.ArtifactURL); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// InsertIssue inserts a new issue and returns its ID.
func (s *Store) InsertIssue(issue *Issue) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO issues (job_run_id, jira_key, jira_url, pr_number, pr_url, pr_state, pr_created_at, merged_at, closed_at, merge_duration_hours)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.JobRunID, issue.JiraKey, issue.JiraURL, issue.PRNumber, issue.PRURL, issue.PRState,
		issue.PRCreatedAt, issue.MergedAt, issue.ClosedAt, issue.MergeDurationHours,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetIssueByJiraKey retrieves an issue by its Jira key.
func (s *Store) GetIssueByJiraKey(jiraKey string) (*Issue, error) {
	return s.scanIssue(s.db.QueryRow(
		`SELECT id, job_run_id, jira_key, jira_url, pr_number, pr_url, pr_state, pr_created_at, merged_at, closed_at, merge_duration_hours
		 FROM issues WHERE jira_key = ?`, jiraKey,
	))
}

// GetIssueByID retrieves an issue by its ID.
func (s *Store) GetIssueByID(id int64) (*Issue, error) {
	return s.scanIssue(s.db.QueryRow(
		`SELECT id, job_run_id, jira_key, jira_url, pr_number, pr_url, pr_state, pr_created_at, merged_at, closed_at, merge_duration_hours
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
		&issue.PRCreatedAt, &issue.MergedAt, &issue.ClosedAt, &issue.MergeDurationHours,
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

// UpdateIssueState updates the PR state, dates, and merge_duration_hours for an issue.
func (s *Store) UpdateIssueState(id int64, prState string, prCreatedAt, mergedAt, closedAt *time.Time, mergeDurationHours *float64) error {
	_, err := s.db.Exec(
		`UPDATE issues SET pr_state = ?, pr_created_at = ?, merged_at = ?, closed_at = ?, merge_duration_hours = ? WHERE id = ?`,
		prState, prCreatedAt, mergedAt, closedAt, mergeDurationHours, id,
	)
	return err
}

// ListIssues returns issues where the effective date (pr_created_at or job_run started_at)
// falls within the given range.
func (s *Store) ListIssues(from, to time.Time) ([]Issue, error) {
	rows, err := s.db.Query(
		`SELECT i.id, i.job_run_id, i.jira_key, i.jira_url, i.pr_number, i.pr_url, i.pr_state, i.pr_created_at, i.merged_at, i.closed_at, i.merge_duration_hours, jr.started_at
		 FROM issues i
		 JOIN job_runs jr ON i.job_run_id = jr.id
		 WHERE COALESCE(i.pr_created_at, jr.started_at) >= ? AND COALESCE(i.pr_created_at, jr.started_at) < ?`,
		from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanIssuesWithStartedAt(rows)
}

func (s *Store) scanIssuesWithStartedAt(rows *sql.Rows) ([]Issue, error) {
	var issues []Issue
	for rows.Next() {
		var issue Issue
		var prURL, prState sql.NullString
		var prNumber sql.NullInt64
		var startedAt time.Time
		err := rows.Scan(
			&issue.ID, &issue.JobRunID, &issue.JiraKey, &issue.JiraURL,
			&prNumber, &prURL, &prState,
			&issue.PRCreatedAt, &issue.MergedAt, &issue.ClosedAt, &issue.MergeDurationHours, &startedAt,
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
		issue.StartedAt = &startedAt
		issues = append(issues, issue)
	}
	return issues, rows.Err()
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
			&issue.PRCreatedAt, &issue.MergedAt, &issue.ClosedAt, &issue.MergeDurationHours,
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

// GetAllIssuesWithPR returns all issues that have a PR URL, regardless of state or age.
func (s *Store) GetAllIssuesWithPR() ([]Issue, error) {
	rows, err := s.db.Query(
		`SELECT id, job_run_id, jira_key, jira_url, pr_number, pr_url, pr_state, pr_created_at, merged_at, closed_at, merge_duration_hours
		 FROM issues WHERE pr_url != '' AND pr_number > 0`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanIssues(rows)
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

// GetReviewCommentsByIssueID retrieves review comments for a given issue, excluding bot authors.
func (s *Store) GetReviewCommentsByIssueID(issueID int64) ([]ReviewComment, error) {
	query := fmt.Sprintf(
		`SELECT rc.id, rc.issue_id, rc.github_comment_id, rc.author, rc.body, rc.created_at, rc.severity, rc.topic, rc.confidence, rc.ai_classified, rc.human_override
		 FROM review_comments rc WHERE rc.issue_id = ? %s ORDER BY rc.id`, commentFilterSQL("rc.author", "rc.body"))
	rows, err := s.db.Query(query, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanReviewComments(rows)
}

// GetUnclassifiedComments returns non-bot comments where severity is NULL.
func (s *Store) GetUnclassifiedComments() ([]ReviewComment, error) {
	query := fmt.Sprintf(
		`SELECT rc.id, rc.issue_id, rc.github_comment_id, rc.author, rc.body, rc.created_at, rc.severity, rc.topic, rc.confidence, rc.ai_classified, rc.human_override
		 FROM review_comments rc WHERE rc.severity IS NULL %s ORDER BY rc.id`, commentFilterSQL("rc.author", "rc.body"))
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanReviewComments(rows)
}

// GetUnclassifiedCommentsWithContext returns unclassified comments with the PR URL from the parent issue.
func (s *Store) GetUnclassifiedCommentsWithContext() ([]CommentWithContext, error) {
	query := fmt.Sprintf(
		`SELECT rc.id, rc.issue_id, rc.github_comment_id, rc.author, rc.body, rc.created_at,
		        rc.severity, rc.topic, rc.confidence, rc.ai_classified, rc.human_override, COALESCE(i.pr_url, '')
		 FROM review_comments rc
		 JOIN issues i ON rc.issue_id = i.id
		 WHERE rc.severity IS NULL %s ORDER BY rc.id`, commentFilterSQL("rc.author", "rc.body"))
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CommentWithContext
	for rows.Next() {
		var c CommentWithContext
		var severity, topic sql.NullString
		var confidence sql.NullFloat64
		var aiClassified, humanOverride int
		err := rows.Scan(
			&c.ID, &c.IssueID, &c.GitHubCommentID, &c.Author, &c.Body, &c.CreatedAt,
			&severity, &topic, &confidence, &aiClassified, &humanOverride, &c.PRURL,
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
		if confidence.Valid {
			c.Confidence = &confidence.Float64
		}
		c.AIClassified = aiClassified != 0
		c.HumanOverride = humanOverride != 0
		results = append(results, c)
	}
	return results, rows.Err()
}

func (s *Store) scanReviewComments(rows *sql.Rows) ([]ReviewComment, error) {
	var comments []ReviewComment
	for rows.Next() {
		var c ReviewComment
		var severity, topic sql.NullString
		var confidence sql.NullFloat64
		var aiClassified, humanOverride int
		err := rows.Scan(
			&c.ID, &c.IssueID, &c.GitHubCommentID, &c.Author, &c.Body, &c.CreatedAt,
			&severity, &topic, &confidence, &aiClassified, &humanOverride,
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
		if confidence.Valid {
			c.Confidence = &confidence.Float64
		}
		c.AIClassified = aiClassified != 0
		c.HumanOverride = humanOverride != 0
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

// GetCommentsByDateRange returns all non-bot comments for issues in the given date range,
// including the PR URL from the parent issue.
func (s *Store) GetCommentsByDateRange(from, to time.Time) ([]CommentWithContext, error) {
	query := fmt.Sprintf(
		`SELECT rc.id, rc.issue_id, rc.github_comment_id, rc.author, rc.body, rc.created_at,
		        rc.severity, rc.topic, rc.confidence, rc.ai_classified, rc.human_override, COALESCE(i.pr_url, '')
		 FROM review_comments rc
		 JOIN issues i ON rc.issue_id = i.id
		 JOIN job_runs jr ON i.job_run_id = jr.id
		 WHERE COALESCE(i.pr_created_at, jr.started_at) >= ? AND COALESCE(i.pr_created_at, jr.started_at) < ?
		 %s
		 ORDER BY rc.created_at DESC`, commentFilterSQL("rc.author", "rc.body"))
	rows, err := s.db.Query(query, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CommentWithContext
	for rows.Next() {
		var c CommentWithContext
		var severity, topic sql.NullString
		var confidence sql.NullFloat64
		var aiClassified, humanOverride int
		err := rows.Scan(
			&c.ID, &c.IssueID, &c.GitHubCommentID, &c.Author, &c.Body, &c.CreatedAt,
			&severity, &topic, &confidence, &aiClassified, &humanOverride, &c.PRURL,
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
		if confidence.Valid {
			c.Confidence = &confidence.Float64
		}
		c.AIClassified = aiClassified != 0
		c.HumanOverride = humanOverride != 0
		results = append(results, c)
	}
	return results, rows.Err()
}

// GetReviewCommentByID retrieves a single review comment by its ID.
func (s *Store) GetReviewCommentByID(id int64) (*ReviewComment, error) {
	row := s.db.QueryRow(
		`SELECT id, issue_id, github_comment_id, author, body, created_at, severity, topic, confidence, ai_classified, human_override
		 FROM review_comments WHERE id = ?`, id,
	)
	var c ReviewComment
	var severity, topic sql.NullString
	var confidence sql.NullFloat64
	var aiClassified, humanOverride int
	err := row.Scan(
		&c.ID, &c.IssueID, &c.GitHubCommentID, &c.Author, &c.Body, &c.CreatedAt,
		&severity, &topic, &confidence, &aiClassified, &humanOverride,
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
	if confidence.Valid {
		c.Confidence = &confidence.Float64
	}
	c.AIClassified = aiClassified != 0
	c.HumanOverride = humanOverride != 0
	return &c, nil
}

// UpdateCommentClassification updates the severity, topic, confidence, and human_override for a comment.
func (s *Store) UpdateCommentClassification(id int64, severity, topic string, confidence *float64, humanOverride bool) error {
	_, err := s.db.Exec(
		`UPDATE review_comments SET severity = ?, topic = ?, confidence = ?, human_override = ? WHERE id = ?`,
		severity, topic, confidence, boolToInt(humanOverride), id,
	)
	return err
}

// UpdateCommentAIClassification updates classification fields and marks the comment as AI-classified.
func (s *Store) UpdateCommentAIClassification(id int64, severity, topic string, confidence *float64) error {
	_, err := s.db.Exec(
		`UPDATE review_comments SET severity = ?, topic = ?, confidence = ?, ai_classified = 1, human_override = 0 WHERE id = ?`,
		severity, topic, confidence, id,
	)
	return err
}

// InsertOrUpdatePRComplexity upserts PR complexity data for an issue.
// Complexity deltas are only overwritten when the incoming values are non-zero,
// so that a PR-stats refresh doesn't wipe previously analyzed complexity.
func (s *Store) InsertOrUpdatePRComplexity(c *PRComplexity) error {
	analyzed := 0
	if c.ComplexityAnalyzed {
		analyzed = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO pr_complexity (issue_id, lines_added, lines_deleted, files_changed, cyclomatic_complexity_delta, cognitive_complexity_delta, complexity_analyzed)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(issue_id) DO UPDATE SET
		   lines_added = excluded.lines_added,
		   lines_deleted = excluded.lines_deleted,
		   files_changed = excluded.files_changed,
		   cyclomatic_complexity_delta = CASE WHEN excluded.complexity_analyzed = 1 THEN excluded.cyclomatic_complexity_delta ELSE pr_complexity.cyclomatic_complexity_delta END,
		   cognitive_complexity_delta = CASE WHEN excluded.complexity_analyzed = 1 THEN excluded.cognitive_complexity_delta ELSE pr_complexity.cognitive_complexity_delta END,
		   complexity_analyzed = MAX(pr_complexity.complexity_analyzed, excluded.complexity_analyzed)`,
		c.IssueID, c.LinesAdded, c.LinesDeleted, c.FilesChanged,
		c.CyclomaticComplexityDelta, c.CognitiveComplexityDelta, analyzed,
	)
	return err
}

// GetPRComplexityByIssueID retrieves PR complexity data for a given issue.
func (s *Store) GetPRComplexityByIssueID(issueID int64) (*PRComplexity, error) {
	row := s.db.QueryRow(
		`SELECT id, issue_id, lines_added, lines_deleted, files_changed, cyclomatic_complexity_delta, cognitive_complexity_delta, complexity_analyzed
		 FROM pr_complexity WHERE issue_id = ?`, issueID,
	)
	var c PRComplexity
	var analyzed int
	err := row.Scan(&c.ID, &c.IssueID, &c.LinesAdded, &c.LinesDeleted, &c.FilesChanged,
		&c.CyclomaticComplexityDelta, &c.CognitiveComplexityDelta, &analyzed)
	if err != nil {
		return nil, err
	}
	c.ComplexityAnalyzed = analyzed != 0
	return &c, nil
}

// GetIssuesNeedingComplexity returns issues that have a pr_complexity row but
// complexity has not been analyzed yet (complexity_analyzed = 0).
func (s *Store) GetIssuesNeedingComplexity() ([]Issue, error) {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	rows, err := s.db.Query(
		`SELECT i.id, i.job_run_id, i.jira_key, i.jira_url, i.pr_number, i.pr_url, i.pr_state, i.pr_created_at, i.merged_at, i.closed_at, i.merge_duration_hours
		 FROM issues i
		 JOIN pr_complexity pc ON i.id = pc.issue_id
		 WHERE i.pr_url != '' AND i.pr_number > 0
		   AND pc.complexity_analyzed = 0
		   AND (i.merged_at IS NULL OR i.merged_at > ?)`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanIssues(rows)
}

// GetIssuesNeedingPRData returns issues that have a PR URL but no pr_complexity row yet.
// Excludes issues merged more than 30 days ago to avoid processing stale data.
func (s *Store) GetIssuesNeedingPRData() ([]Issue, error) {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	rows, err := s.db.Query(
		`SELECT i.id, i.job_run_id, i.jira_key, i.jira_url, i.pr_number, i.pr_url, i.pr_state, i.pr_created_at, i.merged_at, i.closed_at, i.merge_duration_hours
		 FROM issues i
		 LEFT JOIN pr_complexity pc ON i.id = pc.issue_id
		 WHERE i.pr_url != '' AND i.pr_number > 0 AND pc.id IS NULL
		   AND (i.merged_at IS NULL OR i.merged_at > ?)`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanIssues(rows)
}

// GetIssuesMissingPR returns issues with pr_number=0 along with their build_id for re-parsing.
func (s *Store) GetIssuesMissingPR() ([]IssueWithBuildID, error) {
	rows, err := s.db.Query(
		`SELECT i.id, i.jira_key, j.build_id
		 FROM issues i
		 JOIN job_runs j ON i.job_run_id = j.id
		 WHERE i.pr_number = 0 OR i.pr_url = ''`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []IssueWithBuildID
	for rows.Next() {
		var r IssueWithBuildID
		if err := rows.Scan(&r.IssueID, &r.JiraKey, &r.BuildID); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// UpdateIssuePR updates the pr_number and pr_url for an issue.
func (s *Store) UpdateIssuePR(id int64, prNumber int, prURL string) error {
	_, err := s.db.Exec(
		`UPDATE issues SET pr_number = ?, pr_url = ? WHERE id = ?`,
		prNumber, prURL, id,
	)
	return err
}

// GetOpenIssues returns issues where pr_state is not "merged" and not "closed".
func (s *Store) GetOpenIssues() ([]Issue, error) {
	rows, err := s.db.Query(
		`SELECT id, job_run_id, jira_key, jira_url, pr_number, pr_url, pr_state, pr_created_at, merged_at, closed_at, merge_duration_hours
		 FROM issues WHERE pr_state IS NOT NULL AND pr_state != 'merged' AND pr_state != 'closed'`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanIssues(rows)
}

// GetWeeklyTrends returns aggregated weekly statistics for issues within the given time range.
// Quality score (0-100): outcome(40) + severity(35) + density(15) + topics(10)
func (s *Store) GetWeeklyTrends(from, to time.Time) ([]WeeklyTrend, error) {
	query := `
WITH issue_stats AS (
	SELECT
		i.id AS issue_id,
		DATE(COALESCE(i.pr_created_at, jr.started_at), 'weekday 0', '-6 days') AS week_start,
		CASE WHEN i.pr_state = 'merged' THEN 1 ELSE 0 END AS is_merged,
		COALESCE((SELECT SUM(pm.cost_usd) FROM phase_metrics pm WHERE pm.issue_id = i.id), 0) AS total_cost,
		COALESCE((SELECT SUM(pm.duration_ms) FROM phase_metrics pm WHERE pm.issue_id = i.id), 0) AS total_duration,
		COALESCE((SELECT COUNT(*) FROM review_comments rc WHERE rc.issue_id = i.id ` + commentFilterSQL("rc.author", "rc.body") + `), 0) AS comment_count,
		COALESCE((SELECT pc.lines_added + pc.lines_deleted FROM pr_complexity pc WHERE pc.issue_id = i.id), 0) AS lines_changed,
		CASE WHEN i.pr_state = 'merged' THEN 40 WHEN i.pr_state = 'open' THEN 20 ELSE 0 END AS outcome_score,
		COALESCE((SELECT SUM(CASE
			WHEN rc.severity = 'required_change' THEN 8
			WHEN rc.severity = 'question' THEN 4
			WHEN rc.severity = 'suggestion' THEN 2
			WHEN rc.severity = 'nitpick' THEN 1
			ELSE 0 END)
			FROM review_comments rc WHERE rc.issue_id = i.id ` + commentFilterSQL("rc.author", "rc.body") + `), 0) AS severity_penalty,
		COALESCE((SELECT SUM(CASE
			WHEN rc.topic = 'security' THEN 5
			WHEN rc.topic = 'logic_bug' THEN 5
			WHEN rc.topic = 'test_gap' THEN 3
			WHEN rc.topic = 'architecture_design' THEN 2
			WHEN rc.topic = 'style' THEN 1
			ELSE 0 END)
			FROM review_comments rc WHERE rc.issue_id = i.id ` + commentFilterSQL("rc.author", "rc.body") + `), 0) AS topic_penalty
	FROM issues i
	JOIN job_runs jr ON i.job_run_id = jr.id
	WHERE COALESCE(i.pr_created_at, jr.started_at) >= ? AND COALESCE(i.pr_created_at, jr.started_at) < ?
)
SELECT
	week_start,
	COUNT(*) AS total_issues,
	SUM(is_merged) AS merged_issues,
	CAST(SUM(is_merged) AS REAL) / MAX(COUNT(*), 1) AS merge_rate,
	AVG(total_cost) AS avg_cost_usd,
	AVG(total_duration) AS avg_duration_ms,
	AVG(comment_count) AS avg_review_comments,
	AVG(
		outcome_score
		+ MAX(35.0 - severity_penalty, 0)
		+ CASE WHEN lines_changed > 0 AND comment_count > 0
			THEN MAX(15.0 * (1.0 - CAST(comment_count AS REAL) / CAST(lines_changed AS REAL) * 10.0), 0)
			ELSE 15.0 END
		+ MAX(10.0 - topic_penalty, 0)
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
			&weekStart, &tr.TotalIssues, &tr.MergedIssues, &tr.MergeRate,
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

// RecordScraperRun inserts a scraper run record.
func (s *Store) RecordScraperRun(run *ScraperRun) error {
	_, err := s.db.Exec(
		`INSERT INTO scraper_runs (step, started_at, finished_at, status, items_processed) VALUES (?, ?, ?, ?, ?)`,
		run.Step, run.StartedAt, run.FinishedAt, run.Status, run.ItemsProcessed,
	)
	return err
}

// GetLatestScraperRuns returns the most recent run for each step.
func (s *Store) GetLatestScraperRuns() ([]ScraperRun, error) {
	rows, err := s.db.Query(
		`SELECT id, step, started_at, finished_at, status, items_processed
		 FROM scraper_runs
		 WHERE id IN (SELECT MAX(id) FROM scraper_runs GROUP BY step)
		 ORDER BY step`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []ScraperRun
	for rows.Next() {
		var r ScraperRun
		if err := rows.Scan(&r.ID, &r.Step, &r.StartedAt, &r.FinishedAt, &r.Status, &r.ItemsProcessed); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
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
