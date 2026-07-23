package scraper

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

type AutodlEnvelope struct {
	TableName string            `json:"table_name"`
	Schema    map[string]string `json:"schema"`
	Rows      []json.RawMessage `json:"rows"`
}

type SessionMetricsRow struct {
	SessionID                string  `json:"session_id"`
	Model                    string  `json:"model"`
	ClaudeCodeVersion        string  `json:"claude_code_version"`
	PermissionMode           string  `json:"permission_mode"`
	Entrypoint               string  `json:"entrypoint"`
	Prompt                   string  `json:"prompt"`
	PluginsLoaded            string  `json:"plugins_loaded"`
	AnalyzedAt               string  `json:"analyzed_at"`
	DurationMs               string  `json:"duration_ms"`
	DurationAPIMs            string  `json:"duration_api_ms"`
	TTFTMs                   string  `json:"ttft_ms"`
	NumTurns                 string  `json:"num_turns"`
	TotalCostUSD             string  `json:"total_cost_usd"`
	InputTokens              string  `json:"input_tokens"`
	OutputTokens             string  `json:"output_tokens"`
	CacheReadInputTokens     string  `json:"cache_read_input_tokens"`
	CacheCreationInputTokens string  `json:"cache_creation_input_tokens"`
	CacheHitRatePct          string  `json:"cache_hit_rate_pct"`
	TotalToolCalls           string  `json:"total_tool_calls"`
	ToolCallBreakdown        string  `json:"tool_call_breakdown"`
	SkillsInvoked            string  `json:"skills_invoked"`
	FilesWritten             string  `json:"files_written"`
	NumThinkingBlocks        string  `json:"num_thinking_blocks"`
	NumSubagents             string  `json:"num_subagents"`
	SubagentTotalToolUses    string  `json:"subagent_total_tool_uses"`
	SubagentTotalDurationMs  string  `json:"subagent_total_duration_ms"`
	IsError                  string  `json:"is_error"`
	TerminalReason           string  `json:"terminal_reason"`
	StopReason               string  `json:"stop_reason"`
}

type JiraAgentRow struct {
	SessionID  string `json:"session_id"`
	Agent      string `json:"agent"`
	Phase      string `json:"phase"`
	IssueKey   string `json:"issue_key"`
	PRURL      string `json:"pr_url"`
	Result     string `json:"result"`
	AnalyzedAt string `json:"analyzed_at"`
	JobName    string `json:"job_name"`
	BuildID    string `json:"build_id"`
}

func ParseSessionMetrics(data []byte) (*SessionMetricsRow, error) {
	var env AutodlEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling autodl envelope: %w", err)
	}
	if env.TableName != "claude_session_metrics" {
		return nil, fmt.Errorf("unexpected table_name %q, want claude_session_metrics", env.TableName)
	}
	if len(env.Rows) == 0 {
		return nil, fmt.Errorf("no rows in session metrics autodl")
	}
	var row SessionMetricsRow
	if err := json.Unmarshal(env.Rows[0], &row); err != nil {
		return nil, fmt.Errorf("unmarshaling session metrics row: %w", err)
	}
	return &row, nil
}

func ParseJiraAgentRow(data []byte) (*JiraAgentRow, error) {
	var env AutodlEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshaling autodl envelope: %w", err)
	}
	if env.TableName != "jira_agent" {
		return nil, fmt.Errorf("unexpected table_name %q, want jira_agent", env.TableName)
	}
	if len(env.Rows) == 0 {
		return nil, fmt.Errorf("no rows in jira agent autodl")
	}
	var row JiraAgentRow
	if err := json.Unmarshal(env.Rows[0], &row); err != nil {
		return nil, fmt.Errorf("unmarshaling jira agent row: %w", err)
	}
	return &row, nil
}

// sessionMetricsFileRe matches filenames like "claude-OCPBUGS-98086-solve-session-metrics-autodl.json"
// The first capture anchors on the Jira key pattern ([A-Z]+-\d+) to avoid greedy mismatch on multi-word phases like "pr-creation".
var sessionMetricsFileRe = regexp.MustCompile(`^claude-([A-Z]+-\d+)-(.+)-session-metrics-autodl\.json$`)

// jiraAgentFileRe matches filenames like "jira-agent-OCPBUGS-98086-solve-autodl.json"
var jiraAgentFileRe = regexp.MustCompile(`^jira-agent-([A-Z]+-\d+)-(.+)-autodl\.json$`)

// ParseAutodlFilename extracts the issue key and phase from an autodl filename.
// Returns ("", "", false) if the filename doesn't match.
func ParseAutodlFilename(filename string) (issueKey, phase string, isSessionMetrics bool, ok bool) {
	if m := sessionMetricsFileRe.FindStringSubmatch(filename); m != nil {
		return m[1], m[2], true, true
	}
	if m := jiraAgentFileRe.FindStringSubmatch(filename); m != nil {
		return m[1], m[2], false, true
	}
	return "", "", false, false
}

func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func atof64(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
