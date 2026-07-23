package api

// TrendPoint represents aggregated weekly trend data.
type TrendPoint struct {
	WeekStart         string  `json:"week_start"`
	TotalIssues       int     `json:"total_issues"`
	MergedIssues      int     `json:"merged_issues"`
	MergeRate         float64 `json:"merge_rate"`
	AvgCost           float64 `json:"avg_cost"`
	AvgDurationMs     float64 `json:"avg_duration_ms"`
	AvgMergeDuration  float64 `json:"avg_merge_duration"`
	AvgReviewComments float64 `json:"avg_review_comments"`
	AvgQualityScore   float64 `json:"avg_quality_score"`
}

// IssueSummary represents a summarized view of an issue.
type IssueSummary struct {
	ID                 int64    `json:"id"`
	JiraKey            string   `json:"jira_key"`
	JiraURL            string   `json:"jira_url"`
	PRURL              string   `json:"pr_url"`
	PRNumber           int      `json:"pr_number"`
	PRState            string   `json:"pr_state"`
	PRMerged           bool     `json:"pr_merged"`
	PRClosed           bool     `json:"pr_closed"`
	PRCreatedAt        string   `json:"pr_created_at"`
	MergedAt           string   `json:"merged_at"`
	ClosedAt           string   `json:"closed_at"`
	ReviewCommentCount int      `json:"review_comment_count"`
	LinesAdded         int      `json:"lines_added"`
	LinesDeleted       int      `json:"lines_deleted"`
	LinesChanged       int      `json:"lines_changed"`
	FilesChanged       int      `json:"files_changed"`
	ComplexityDelta    float64  `json:"complexity_delta"`
	TotalCost          float64  `json:"total_cost"`
	MergeDuration      *float64 `json:"merge_duration"`
	QualityScore       float64  `json:"quality_score"`
	ReviewCycles       int      `json:"review_cycles"`
	CreatedAt          string   `json:"created_at"`
	ArtifactURL        string   `json:"artifact_url"`
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
	ID              int64    `json:"id"`
	IssueID         int64    `json:"issue_id,omitempty"`
	GitHubCommentID int64    `json:"github_comment_id,omitempty"`
	Author          string   `json:"author"`
	Body            string   `json:"body"`
	CreatedAt       string   `json:"created_at"`
	Severity        string   `json:"severity"`
	Topic           string   `json:"topic"`
	Confidence      *float64 `json:"confidence"`
	AIClassified    bool     `json:"ai_classified"`
	HumanOverride   bool     `json:"human_override"`
	PRURL           string   `json:"pr_url,omitempty"`
}

// ClassificationUpdate represents a request to update comment classification.
type ClassificationUpdate struct {
	Severity   string   `json:"severity"`
	Topic      string   `json:"topic"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// ScraperStatus represents the most recent run of a scraper step.
type ScraperStatus struct {
	Step           string `json:"step"`
	FinishedAt     string `json:"finished_at"`
	Status         string `json:"status"`
	ItemsProcessed int    `json:"items_processed"`
}

// OutcomeTrendPoint represents a cumulative outcome bucket for the API.
type OutcomeTrendPoint struct {
	WeekStart string `json:"week_start"`
	Merged    int    `json:"merged"`
	Closed    int    `json:"closed"`
	CumMerged int    `json:"cum_merged"`
	CumClosed int    `json:"cum_closed"`
}

// TelemetryRow represents a single session telemetry record for the API.
type TelemetryRow struct {
	ID                       int64   `json:"id"`
	IssueKey                 string  `json:"issue_key"`
	Phase                    string  `json:"phase"`
	SessionID                string  `json:"session_id"`
	Result                   string  `json:"result"`
	Model                    string  `json:"model"`
	ClaudeCodeVersion        string  `json:"claude_code_version"`
	DurationMs               int64   `json:"duration_ms"`
	DurationAPIMs            int64   `json:"duration_api_ms"`
	TTFTMs                   int64   `json:"ttft_ms"`
	NumTurns                 int64   `json:"num_turns"`
	TotalCostUSD             float64 `json:"total_cost_usd"`
	InputTokens              int64   `json:"input_tokens"`
	OutputTokens             int64   `json:"output_tokens"`
	CacheReadInputTokens     int64   `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64   `json:"cache_creation_input_tokens"`
	CacheHitRatePct          float64 `json:"cache_hit_rate_pct"`
	TotalToolCalls           int64   `json:"total_tool_calls"`
	ToolCallBreakdown        string  `json:"tool_call_breakdown"`
	FilesWritten             int64   `json:"files_written"`
	NumThinkingBlocks        int64   `json:"num_thinking_blocks"`
	NumSubagents             int64   `json:"num_subagents"`
	SubagentTotalToolUses    int64   `json:"subagent_total_tool_uses"`
	SubagentTotalDurationMs  int64   `json:"subagent_total_duration_ms"`
	IsError                  int64   `json:"is_error"`
	TerminalReason           string  `json:"terminal_reason"`
	StopReason               string  `json:"stop_reason"`
	StartedAt                string  `json:"started_at"`
}
