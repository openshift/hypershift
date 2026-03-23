package api

// TrendPoint represents aggregated weekly trend data.
type TrendPoint struct {
	WeekStart         string  `json:"week_start"`
	IssuesProcessed   int     `json:"issues_processed"`
	MergeRate         float64 `json:"merge_rate"`
	AvgCostUSD        float64 `json:"avg_cost_usd"`
	AvgDurationMs     float64 `json:"avg_duration_ms"`
	AvgReviewComments float64 `json:"avg_review_comments"`
	AvgQualityScore   float64 `json:"avg_quality_score"`
}

// IssueSummary represents a summarized view of an issue.
type IssueSummary struct {
	ID              int64   `json:"id"`
	JiraKey         string  `json:"jira_key"`
	JiraURL         string  `json:"jira_url"`
	PRURL           string  `json:"pr_url"`
	PRState         string  `json:"pr_state"`
	ReviewComments  int     `json:"review_comments"`
	LinesChanged    int     `json:"lines_changed"`
	FilesChanged    int     `json:"files_changed"`
	ComplexityDelta float64 `json:"complexity_delta"`
	CostUSD         float64 `json:"cost_usd"`
	DurationMs      int64   `json:"duration_ms"`
	QualityScore    float64 `json:"quality_score"`
	CreatedAt       string  `json:"created_at"`
}

// IssueDetail represents the full detail view of an issue.
type IssueDetail struct {
	IssueSummary
	Phases   []PhaseDetail   `json:"phases"`
	Comments []CommentDetail `json:"comments"`
}

// PhaseDetail represents a single phase metric for an issue.
type PhaseDetail struct {
	Phase               string  `json:"phase"`
	Status              string  `json:"status"`
	DurationMs          int64   `json:"duration_ms"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CostUSD             float64 `json:"cost_usd"`
	Model               string  `json:"model"`
	TurnCount           int     `json:"turn_count"`
}

// CommentDetail represents a review comment with classification details.
type CommentDetail struct {
	ID            int64  `json:"id"`
	Author        string `json:"author"`
	Body          string `json:"body"`
	CreatedAt     string `json:"created_at"`
	Severity      string `json:"severity"`
	Topic         string `json:"topic"`
	AIClassified  bool   `json:"ai_classified"`
	HumanOverride bool   `json:"human_override"`
}

// ClassificationUpdate represents a request to update comment classification.
type ClassificationUpdate struct {
	Severity string `json:"severity"`
	Topic    string `json:"topic"`
}
