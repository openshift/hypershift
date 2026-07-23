package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/openshift/jira-agent-dashboard/internal/db"
)

var (
	allowedSeverities = map[string]bool{
		"nitpick":         true,
		"suggestion":      true,
		"required_change": true,
		"question":        true,
		"unclassified":    true,
	}
	allowedTopics = map[string]bool{
		"style":               true,
		"logic_bug":           true,
		"test_gap":            true,
		"api_design":          true,
		"architecture_design": true,
		"security":            true,
		"documentation":       true,
		"ci":                  true,
		"approval":            true,
		"process":             true,
		"unclassified":        true,
	}
)

func (s *Server) handleGetTrends(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	granularity := r.URL.Query().Get("granularity")
	if granularity == "" {
		granularity = "weekly"
	}
	if granularity != "daily" && granularity != "weekly" {
		http.Error(w, "granularity must be 'daily' or 'weekly'", http.StatusBadRequest)
		return
	}

	trends, err := s.store.GetTrends(from, to, granularity)
	if err != nil {
		internalError(w, "fetching trends", err)
		return
	}

	result := make([]TrendPoint, len(trends))
	for i, t := range trends {
		result[i] = TrendPoint{
			WeekStart:         t.BucketStart.Format("2006-01-02"),
			TotalIssues:       t.TotalIssues,
			MergedIssues:      t.MergedIssues,
			MergeRate:         t.MergeRate,
			AvgCost:           t.AvgCostUSD,
			AvgDurationMs:     t.AvgDurationMs,
			AvgMergeDuration:  t.AvgDurationMs,
			AvgReviewComments: t.AvgReviewComments,
			AvgQualityScore:   t.AvgQualityScore,
		}
	}

	writeJSON(w, result)
}

func (s *Server) handleGetOutcomeTrends(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	granularity := r.URL.Query().Get("granularity")
	if granularity == "" {
		granularity = "weekly"
	}
	if granularity != "daily" && granularity != "weekly" {
		http.Error(w, "granularity must be 'daily' or 'weekly'", http.StatusBadRequest)
		return
	}

	buckets, err := s.store.GetCumulativeOutcomes(from, to, granularity)
	if err != nil {
		internalError(w, "fetching outcome trends", err)
		return
	}

	result := make([]OutcomeTrendPoint, len(buckets))
	for i, b := range buckets {
		result[i] = OutcomeTrendPoint{
			WeekStart: b.BucketStart.Format("2006-01-02"),
			Merged:    b.Merged,
			Closed:    b.Closed,
			CumMerged: b.CumMerged,
			CumClosed: b.CumClosed,
		}
	}

	writeJSON(w, result)
}

func (s *Server) handleGetIssues(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	issues, err := s.store.ListIssues(from, to)
	if err != nil {
		internalError(w, "listing issues", err)
		return
	}

	result := make([]IssueSummary, 0, len(issues))
	for _, issue := range issues {
		comments, err := s.store.GetReviewCommentsByIssueID(issue.ID)
		if err != nil {
			internalError(w, "fetching comments", err)
			return
		}

		complexity, _ := s.store.GetPRComplexityByIssueID(issue.ID)
		phases, err := s.store.GetPhaseMetricsByIssueID(issue.ID)
		if err != nil {
			internalError(w, "fetching phase metrics", err)
			return
		}

		result = append(result, buildIssueSummary(issue, comments, complexity, phases))
	}

	writeJSON(w, result)
}

func (s *Server) handleGetIssueDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid issue id", http.StatusBadRequest)
		return
	}

	issue, err := s.store.GetIssueByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "issue not found", http.StatusNotFound)
			return
		}
		internalError(w, "fetching issue", err)
		return
	}

	phases, err := s.store.GetPhaseMetricsByIssueID(id)
	if err != nil {
		internalError(w, "fetching phase metrics", err)
		return
	}

	comments, err := s.store.GetReviewCommentsByIssueID(id)
	if err != nil {
		internalError(w, "fetching comments", err)
		return
	}

	complexity, _ := s.store.GetPRComplexityByIssueID(id)

	phaseDetails := make([]PhaseDetail, len(phases))
	for i, p := range phases {
		phaseDetails[i] = PhaseDetail{
			Phase:               p.Phase,
			Status:              p.Status,
			DurationMs:          p.DurationMs,
			InputTokens:         p.InputTokens,
			OutputTokens:        p.OutputTokens,
			CacheReadTokens:     p.CacheReadTokens,
			CacheCreationTokens: p.CacheCreationTokens,
			CostUSD:             p.CostUSD,
			Model:               p.Model,
			TurnCount:           p.TurnCount,
		}
	}

	commentDetails := make([]CommentDetail, len(comments))
	for i, c := range comments {
		commentDetails[i] = CommentDetail{
			ID:              c.ID,
			GitHubCommentID: c.GitHubCommentID,
			Author:          c.Author,
			Body:            c.Body,
			CreatedAt:       c.CreatedAt.Format("2006-01-02T15:04:05Z"),
			Severity:        c.Severity,
			Topic:           c.Topic,
			Confidence:      c.Confidence,
			AIClassified:    c.AIClassified,
			HumanOverride:   c.HumanOverride,
		}
	}

	detail := IssueDetail{
		IssueSummary: buildIssueSummary(*issue, comments, complexity, phases),
		Phases:       phaseDetails,
		Comments:     commentDetails,
	}

	writeJSON(w, detail)
}

func (s *Server) handleGetCommentsSummary(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	comments, err := s.store.GetCommentsByDateRange(from, to)
	if err != nil {
		internalError(w, "fetching comments summary", err)
		return
	}

	result := make([]CommentDetail, len(comments))
	for i, c := range comments {
		result[i] = CommentDetail{
			ID:              c.ID,
			IssueID:         c.IssueID,
			GitHubCommentID: c.GitHubCommentID,
			Author:          c.Author,
			Body:            c.Body,
			CreatedAt:       c.CreatedAt.Format("2006-01-02T15:04:05Z"),
			Severity:        c.Severity,
			Topic:           c.Topic,
			Confidence:      c.Confidence,
			AIClassified:    c.AIClassified,
			HumanOverride:   c.HumanOverride,
			PRURL:           c.PRURL,
		}
	}

	writeJSON(w, result)
}

func (s *Server) handleGetComments(w http.ResponseWriter, r *http.Request) {
	issueID, err := strconv.ParseInt(r.PathValue("issueID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid issue id", http.StatusBadRequest)
		return
	}

	comments, err := s.store.GetReviewCommentsByIssueID(issueID)
	if err != nil {
		internalError(w, "fetching comments", err)
		return
	}

	result := make([]CommentDetail, len(comments))
	for i, c := range comments {
		result[i] = CommentDetail{
			ID:              c.ID,
			GitHubCommentID: c.GitHubCommentID,
			Author:          c.Author,
			Body:            c.Body,
			CreatedAt:       c.CreatedAt.Format("2006-01-02T15:04:05Z"),
			Severity:        c.Severity,
			Topic:           c.Topic,
			Confidence:      c.Confidence,
			AIClassified:    c.AIClassified,
			HumanOverride:   c.HumanOverride,
		}
	}

	writeJSON(w, result)
}

func (s *Server) handlePatchComment(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Forwarded-User") == "" {
		http.Error(w, "authentication required", http.StatusForbidden)
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid comment id", http.StatusBadRequest)
		return
	}

	// Limit request body to 1 KB to prevent abuse.
	r.Body = http.MaxBytesReader(w, r.Body, 1024)

	var update ClassificationUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if !allowedSeverities[update.Severity] {
		http.Error(w, "invalid severity value", http.StatusBadRequest)
		return
	}
	if !allowedTopics[update.Topic] {
		http.Error(w, "invalid topic value", http.StatusBadRequest)
		return
	}

	if err := s.store.UpdateCommentClassification(id, update.Severity, update.Topic, update.Confidence, true); err != nil {
		internalError(w, "updating classification", err)
		return
	}

	// Fetch updated comment to return.
	comment, err := s.store.GetReviewCommentByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "comment not found", http.StatusNotFound)
			return
		}
		internalError(w, "fetching comment", err)
		return
	}

	result := CommentDetail{
		ID:              comment.ID,
		GitHubCommentID: comment.GitHubCommentID,
		Author:          comment.Author,
		Body:            comment.Body,
		CreatedAt:       comment.CreatedAt.Format("2006-01-02T15:04:05Z"),
		Severity:        comment.Severity,
		Topic:           comment.Topic,
		Confidence:      comment.Confidence,
		AIClassified:    comment.AIClassified,
		HumanOverride:   comment.HumanOverride,
	}

	writeJSON(w, result)
}

// calculateQualityScore computes a 0–100 quality score for an AI-generated PR.
//
// Components:
//   - Outcome (40 pts):  merged=40, open=20, closed=0
//   - Severity (35 pts): start at 35, deduct per comment severity
//   - Density  (15 pts): fewer comments per 100 lines changed = better
//   - Topics   (10 pts): deduct for logic bugs and test gaps found
func calculateQualityScore(prState string, comments []db.ReviewComment, linesChanged int) float64 {
	// Outcome component (0–40)
	var outcome float64
	switch prState {
	case "merged":
		outcome = 40
	case "open":
		outcome = 20
	default: // closed
		outcome = 0
	}

	// Severity component (0–35)
	severity := 35.0
	for _, c := range comments {
		switch c.Severity {
		case "required_change":
			severity -= 8
		case "question":
			severity -= 4
		case "suggestion":
			severity -= 2
		case "nitpick":
			severity -= 1
		}
	}
	if severity < 0 {
		severity = 0
	}

	// Density component (0–15): comments per 100 lines changed
	density := 15.0
	if linesChanged > 0 && len(comments) > 0 {
		commentsPerHundred := float64(len(comments)) / float64(linesChanged) * 100
		// Scale: 0 comments/100 lines = 15, 10+ comments/100 lines = 0
		density = 15 * (1 - commentsPerHundred/10)
		if density < 0 {
			density = 0
		}
	}

	// Topic component (0–10)
	topics := 10.0
	for _, c := range comments {
		switch c.Topic {
		case "security":
			topics -= 5
		case "logic_bug":
			topics -= 5
		case "test_gap":
			topics -= 3
		case "architecture_design":
			topics -= 2
		case "style":
			topics -= 1
		}
	}
	if topics < 0 {
		topics = 0
	}

	return outcome + severity + density + topics
}

func (s *Server) handleGetScraperStatus(w http.ResponseWriter, _ *http.Request) {
	runs, err := s.store.GetLatestScraperRuns()
	if err != nil {
		internalError(w, "fetching scraper status", err)
		return
	}

	result := make([]ScraperStatus, len(runs))
	for i, r := range runs {
		result[i] = ScraperStatus{
			Step:           r.Step,
			FinishedAt:     r.FinishedAt.Format("2006-01-02T15:04:05Z"),
			Status:         r.Status,
			ItemsProcessed: r.ItemsProcessed,
		}
	}

	writeJSON(w, result)
}

func (s *Server) handleGetTelemetry(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rows, err := s.store.ListSessionTelemetry(from, to)
	if err != nil {
		internalError(w, "fetching telemetry", err)
		return
	}

	result := make([]TelemetryRow, len(rows))
	for i, t := range rows {
		result[i] = telemetryToRow(t)
	}

	writeJSON(w, result)
}

func (s *Server) handleGetTelemetrySummary(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	summary, err := s.store.GetTelemetrySummary(from, to)
	if err != nil {
		internalError(w, "fetching telemetry summary", err)
		return
	}

	writeJSON(w, summary)
}

func (s *Server) handleGetTelemetryByIssue(w http.ResponseWriter, r *http.Request) {
	issueKey := r.PathValue("issueKey")
	if issueKey == "" {
		http.Error(w, "missing issue key", http.StatusBadRequest)
		return
	}

	rows, err := s.store.GetSessionTelemetryByIssue(issueKey)
	if err != nil {
		internalError(w, "fetching telemetry for issue", err)
		return
	}

	result := make([]TelemetryRow, len(rows))
	for i, t := range rows {
		result[i] = telemetryToRow(t)
	}

	writeJSON(w, result)
}

func (s *Server) handleGetOtelToolStats(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	stats, err := s.store.GetOtelToolStats(from, to)
	if err != nil {
		internalError(w, "fetching otel tool stats", err)
		return
	}
	writeJSON(w, stats)
}

func (s *Server) handleGetOtelAPILatencies(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	points, err := s.store.GetOtelAPILatencies(from, to)
	if err != nil {
		internalError(w, "fetching otel api latencies", err)
		return
	}
	writeJSON(w, points)
}

func telemetryToRow(t db.SessionTelemetry) TelemetryRow {
	return TelemetryRow{
		ID:                       t.ID,
		IssueKey:                 t.IssueKey,
		Phase:                    t.Phase,
		SessionID:                t.SessionID,
		Result:                   t.Result,
		Model:                    t.Model,
		ClaudeCodeVersion:        t.ClaudeCodeVersion,
		DurationMs:               t.DurationMs,
		DurationAPIMs:            t.DurationAPIMs,
		TTFTMs:                   t.TTFTMs,
		NumTurns:                 t.NumTurns,
		TotalCostUSD:             t.TotalCostUSD,
		InputTokens:              t.InputTokens,
		OutputTokens:             t.OutputTokens,
		CacheReadInputTokens:     t.CacheReadInputTokens,
		CacheCreationInputTokens: t.CacheCreationInputTokens,
		CacheHitRatePct:          t.CacheHitRatePct,
		TotalToolCalls:           t.TotalToolCalls,
		ToolCallBreakdown:        t.ToolCallBreakdown,
		FilesWritten:             t.FilesWritten,
		NumThinkingBlocks:        t.NumThinkingBlocks,
		NumSubagents:             t.NumSubagents,
		SubagentTotalToolUses:    t.SubagentTotalToolUses,
		SubagentTotalDurationMs:  t.SubagentTotalDurationMs,
		IsError:                  t.IsError,
		TerminalReason:           t.TerminalReason,
		StopReason:               t.StopReason,
		StartedAt:                formatOptionalTime(t.StartedAt),
	}
}

func parseDateRange(r *http.Request) (time.Time, time.Time, error) {
	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid 'from' date, expected format: 2006-01-02")
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid 'to' date, expected format: 2006-01-02")
	}
	// Add one day so "to" is an exclusive upper bound covering the entire final day.
	// The SQL query uses "started_at < ?", so to=2026-03-26 must become 2026-03-27
	// to include data from March 26.
	to = to.AddDate(0, 0, 1)
	return from, to, nil
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02T15:04:05Z")
}

func buildIssueSummary(issue db.Issue, comments []db.ReviewComment, complexity *db.PRComplexity, phases []db.PhaseMetric) IssueSummary {
	var totalCost float64
	var reviewCycles int
	for _, p := range phases {
		totalCost += p.CostUSD
		if p.Phase == "fix" {
			reviewCycles++
		}
	}

	var linesAdded, linesDeleted, filesChanged int
	var complexityDelta float64
	if complexity != nil {
		linesAdded = complexity.LinesAdded
		linesDeleted = complexity.LinesDeleted
		filesChanged = complexity.FilesChanged
		complexityDelta = complexity.CyclomaticComplexityDelta
	}

	return IssueSummary{
		ID:                 issue.ID,
		JiraKey:            issue.JiraKey,
		JiraURL:            issue.JiraURL,
		PRURL:              issue.PRURL,
		PRNumber:           issue.PRNumber,
		PRState:            issue.PRState,
		PRMerged:           issue.PRState == "merged",
		PRClosed:           issue.PRState == "closed",
		PRCreatedAt:        formatOptionalTime(issue.PRCreatedAt),
		MergedAt:           formatOptionalTime(issue.MergedAt),
		ClosedAt:           formatOptionalTime(issue.ClosedAt),
		ReviewCommentCount: len(comments),
		LinesAdded:         linesAdded,
		LinesDeleted:       linesDeleted,
		LinesChanged:       linesAdded + linesDeleted,
		FilesChanged:       filesChanged,
		ComplexityDelta:    complexityDelta,
		TotalCost:          totalCost,
		MergeDuration:      issue.MergeDurationHours,
		QualityScore:       calculateQualityScore(issue.PRState, comments, linesAdded+linesDeleted),
		ReviewCycles:       reviewCycles,
		CreatedAt:          formatOptionalTime(issue.StartedAt),
		ArtifactURL:        issue.ArtifactURL,
		Component:          issue.JobName,
	}
}

// internalError logs the real error server-side and returns a generic message to the client.
func internalError(w http.ResponseWriter, context string, err error) {
	log.Printf("ERROR %s: %v", context, err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
