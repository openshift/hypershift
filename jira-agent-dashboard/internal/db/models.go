package db

import "time"

type JobRun struct {
	ID          int64
	ProwJobID   string
	BuildID     string
	StartedAt   time.Time
	FinishedAt  time.Time
	Status      string // success / failure
	ArtifactURL string
	JobName     string // hypershift / installer
}

type Issue struct {
	ID                 int64
	JobRunID           int64
	JiraKey            string
	JiraURL            string
	PRNumber           int
	PRURL              string
	PRState            string // open / merged / closed
	PRCreatedAt        *time.Time
	MergedAt           *time.Time
	ClosedAt           *time.Time
	MergeDurationHours *float64
	StartedAt          *time.Time // from job_runs, populated by ListIssues
	ArtifactURL        string     // from job_runs, populated by ListIssues
	JobName            string     // from job_runs, populated by ListIssues
}

type PhaseMetric struct {
	ID                  int64
	IssueID             int64
	Phase               string // solve / review / fix / pr
	Status              string // success / failure
	DurationMs          int64
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	CostUSD             float64
	Model               string
	TurnCount           int
	ErrorText           *string
}

type ReviewComment struct {
	ID              int64
	IssueID         int64
	GitHubCommentID int64
	Author          string
	Body            string
	CreatedAt       time.Time
	Severity        string // nitpick / suggestion / required_change / question
	Topic           string // style / logic_bug / test_gap / api_design / documentation
	Confidence      *float64
	AIClassified    bool
	HumanOverride   bool
}

// CommentWithContext extends ReviewComment with issue context for summary views.
type CommentWithContext struct {
	ReviewComment
	PRURL string
}

type PRComplexity struct {
	ID                        int64
	IssueID                   int64
	LinesAdded                int
	LinesDeleted              int
	FilesChanged              int
	CyclomaticComplexityDelta float64
	CognitiveComplexityDelta  float64
	ComplexityAnalyzed        bool
}

type IssueWithBuildID struct {
	IssueID int64
	JiraKey string
	BuildID string
	JobName string
}

type ScraperRun struct {
	ID             int64
	Step           string
	StartedAt      time.Time
	FinishedAt     time.Time
	Status         string
	ItemsProcessed int
}

type SessionTelemetry struct {
	ID                       int64
	JobRunID                 int64
	IssueKey                 string
	Phase                    string
	SessionID                string
	Result                   string
	Model                    string
	ClaudeCodeVersion        string
	Prompt                   string
	DurationMs               int64
	DurationAPIMs            int64
	TTFTMs                   int64
	NumTurns                 int64
	TotalCostUSD             float64
	InputTokens              int64
	OutputTokens             int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
	CacheHitRatePct          float64
	TotalToolCalls           int64
	ToolCallBreakdown        string
	SkillsInvoked            string
	FilesWritten             int64
	NumThinkingBlocks        int64
	NumSubagents             int64
	SubagentTotalToolUses    int64
	SubagentTotalDurationMs  int64
	IsError                  int64
	TerminalReason           string
	StopReason               string
	AnalyzedAt               *time.Time
	StartedAt                *time.Time // from job_runs join, populated by list queries
}

type TelemetrySummary struct {
	TotalSessions      int     `json:"total_sessions"`
	AvgCostUSD         float64 `json:"avg_cost_usd"`
	AvgCacheHitRatePct float64 `json:"avg_cache_hit_rate_pct"`
	AvgTTFTMs          float64 `json:"avg_ttft_ms"`
	AvgToolCalls       float64 `json:"avg_tool_calls"`
	AvgSubagents       float64 `json:"avg_subagents"`
	AvgDurationMs      float64 `json:"avg_duration_ms"`
	AvgNumTurns        float64 `json:"avg_num_turns"`
}

type OtelEvent struct {
	ID                int64
	JobRunID          int64
	SessionID         string
	EventType         string
	TimestampMs       int64
	Model             string
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheCreateTokens int64
	CostUSD           float64
	DurationMs        int64
	ToolName          string
	ToolSuccess       int64
	ToolInputSize     int64
	ToolResultSize    int64
	AgentType         string
	TotalTokens       int64
	TotalToolUses     int64
}

type ToolStat struct {
	ToolName    string  `json:"tool_name"`
	TotalCalls  int64   `json:"total_calls"`
	AvgDuration float64 `json:"avg_duration_ms"`
	SuccessRate float64 `json:"success_rate"`
}

type APILatencyPoint struct {
	DurationMs int64   `json:"duration_ms"`
	CostUSD    float64 `json:"cost_usd"`
	Model      string  `json:"model"`
}

type TrendBucket struct {
	BucketStart       time.Time
	TotalIssues       int
	MergedIssues      int
	MergeRate         float64
	AvgCostUSD        float64
	AvgDurationMs     float64
	AvgReviewComments float64
	AvgQualityScore   float64
}

type CumulativeBucket struct {
	BucketStart time.Time
	Merged      int
	Closed      int
	CumMerged   int
	CumClosed   int
}
