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
}

type Issue struct {
	ID                 int64
	JobRunID           int64
	JiraKey            string
	JiraURL            string
	PRNumber           int
	PRURL              string
	PRState            string // open / merged / closed
	MergedAt           *time.Time
	MergeDurationHours *float64
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
	AIClassified    bool
	HumanOverride   bool
}

type PRComplexity struct {
	ID                        int64
	IssueID                   int64
	LinesAdded                int
	LinesDeleted              int
	FilesChanged              int
	CyclomaticComplexityDelta float64
	CognitiveComplexityDelta  float64
}

type WeeklyTrend struct {
	WeekStart         time.Time
	IssuesProcessed   int
	MergeRate         float64
	AvgCostUSD        float64
	AvgDurationMs     float64
	AvgReviewComments float64
	AvgQualityScore   float64
}
