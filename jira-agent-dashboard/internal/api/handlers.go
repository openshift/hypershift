package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

var (
	allowedSeverities = map[string]bool{
		"nitpick":         true,
		"suggestion":      true,
		"required_change": true,
		"question":        true,
	}
	allowedTopics = map[string]bool{
		"style":         true,
		"logic_bug":     true,
		"test_gap":      true,
		"api_design":    true,
		"documentation": true,
	}
)

func (s *Server) handleGetTrends(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	trends, err := s.store.GetWeeklyTrends(from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]TrendPoint, len(trends))
	for i, t := range trends {
		result[i] = TrendPoint{
			WeekStart:         t.WeekStart.Format("2006-01-02"),
			IssuesProcessed:   t.IssuesProcessed,
			MergeRate:         t.MergeRate,
			AvgCostUSD:        t.AvgCostUSD,
			AvgDurationMs:     t.AvgDurationMs,
			AvgReviewComments: t.AvgReviewComments,
			AvgQualityScore:   t.AvgQualityScore,
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]IssueSummary, 0, len(issues))
	for _, issue := range issues {
		comments, err := s.store.GetReviewCommentsByIssueID(issue.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		complexity, _ := s.store.GetPRComplexityByIssueID(issue.ID)
		phases, err := s.store.GetPhaseMetricsByIssueID(issue.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var totalCost float64
		var totalDuration int64
		for _, p := range phases {
			totalCost += p.CostUSD
			totalDuration += p.DurationMs
		}

		var linesChanged, filesChanged int
		var complexityDelta float64
		if complexity != nil {
			linesChanged = complexity.LinesAdded + complexity.LinesDeleted
			filesChanged = complexity.FilesChanged
			complexityDelta = complexity.CyclomaticComplexityDelta
		}

		// Quality score: review_comments / (max(lines,1) * max(files,1) * max(complexity,1))
		qualityScore := float64(len(comments)) / (max1(float64(linesChanged)) * max1(float64(filesChanged)) * max1(complexityDelta))

		summary := IssueSummary{
			ID:              issue.ID,
			JiraKey:         issue.JiraKey,
			JiraURL:         issue.JiraURL,
			PRURL:           issue.PRURL,
			PRState:         issue.PRState,
			ReviewComments:  len(comments),
			LinesChanged:    linesChanged,
			FilesChanged:    filesChanged,
			ComplexityDelta: complexityDelta,
			CostUSD:         totalCost,
			DurationMs:      totalDuration,
			QualityScore:    qualityScore,
			CreatedAt:       formatOptionalTime(issue.MergedAt),
		}
		result = append(result, summary)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	phases, err := s.store.GetPhaseMetricsByIssueID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	comments, err := s.store.GetReviewCommentsByIssueID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	complexity, _ := s.store.GetPRComplexityByIssueID(id)

	var totalCost float64
	var totalDuration int64
	for _, p := range phases {
		totalCost += p.CostUSD
		totalDuration += p.DurationMs
	}

	var linesChanged, filesChanged int
	var complexityDelta float64
	if complexity != nil {
		linesChanged = complexity.LinesAdded + complexity.LinesDeleted
		filesChanged = complexity.FilesChanged
		complexityDelta = complexity.CyclomaticComplexityDelta
	}

	qualityScore := float64(len(comments)) / (max1(float64(linesChanged)) * max1(float64(filesChanged)) * max1(complexityDelta))

	createdAt := formatOptionalTime(issue.MergedAt)

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
			ID:            c.ID,
			Author:        c.Author,
			Body:          c.Body,
			CreatedAt:     c.CreatedAt.Format("2006-01-02T15:04:05Z"),
			Severity:      c.Severity,
			Topic:         c.Topic,
			AIClassified:  c.AIClassified,
			HumanOverride: c.HumanOverride,
		}
	}

	detail := IssueDetail{
		IssueSummary: IssueSummary{
			ID:              issue.ID,
			JiraKey:         issue.JiraKey,
			JiraURL:         issue.JiraURL,
			PRURL:           issue.PRURL,
			PRState:         issue.PRState,
			ReviewComments:  len(comments),
			LinesChanged:    linesChanged,
			FilesChanged:    filesChanged,
			ComplexityDelta: complexityDelta,
			CostUSD:         totalCost,
			DurationMs:      totalDuration,
			QualityScore:    qualityScore,
			CreatedAt:       createdAt,
		},
		Phases:   phaseDetails,
		Comments: commentDetails,
	}

	writeJSON(w, detail)
}

func (s *Server) handleGetComments(w http.ResponseWriter, r *http.Request) {
	issueID, err := strconv.ParseInt(r.PathValue("issueID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid issue id", http.StatusBadRequest)
		return
	}

	comments, err := s.store.GetReviewCommentsByIssueID(issueID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]CommentDetail, len(comments))
	for i, c := range comments {
		result[i] = CommentDetail{
			ID:            c.ID,
			Author:        c.Author,
			Body:          c.Body,
			CreatedAt:     c.CreatedAt.Format("2006-01-02T15:04:05Z"),
			Severity:      c.Severity,
			Topic:         c.Topic,
			AIClassified:  c.AIClassified,
			HumanOverride: c.HumanOverride,
		}
	}

	writeJSON(w, result)
}

func (s *Server) handlePatchComment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid comment id", http.StatusBadRequest)
		return
	}

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

	if err := s.store.UpdateCommentClassification(id, update.Severity, update.Topic, true); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Fetch updated comment to return.
	comment, err := s.store.GetReviewCommentByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "comment not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := CommentDetail{
		ID:            comment.ID,
		Author:        comment.Author,
		Body:          comment.Body,
		CreatedAt:     comment.CreatedAt.Format("2006-01-02T15:04:05Z"),
		Severity:      comment.Severity,
		Topic:         comment.Topic,
		AIClassified:  comment.AIClassified,
		HumanOverride: comment.HumanOverride,
	}

	writeJSON(w, result)
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
	return from, to, nil
}

func writeJSON(w http.ResponseWriter, data interface{}) {
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

func max1(v float64) float64 {
	if v < 1 {
		return 1
	}
	return v
}
