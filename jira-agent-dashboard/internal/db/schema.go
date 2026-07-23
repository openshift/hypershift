package db

import (
	"database/sql"
	"fmt"
	"strings"
)

func InitSchema(db *sql.DB) error {
	_, err := db.Exec("PRAGMA journal_mode=WAL")
	if err != nil {
		return err
	}

	schema := `
CREATE TABLE IF NOT EXISTS job_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	prow_job_id TEXT NOT NULL,
	build_id TEXT NOT NULL UNIQUE,
	started_at DATETIME,
	finished_at DATETIME,
	status TEXT NOT NULL,
	artifact_url TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS issues (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	job_run_id INTEGER NOT NULL REFERENCES job_runs(id),
	jira_key TEXT NOT NULL,
	jira_url TEXT NOT NULL,
	pr_number INTEGER,
	pr_url TEXT,
	pr_state TEXT,
	pr_created_at DATETIME,
	merged_at DATETIME,
	closed_at DATETIME,
	merge_duration_hours REAL
);

CREATE TABLE IF NOT EXISTS phase_metrics (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	issue_id INTEGER NOT NULL REFERENCES issues(id),
	phase TEXT NOT NULL,
	status TEXT NOT NULL,
	duration_ms INTEGER,
	input_tokens INTEGER,
	output_tokens INTEGER,
	cache_read_tokens INTEGER,
	cache_creation_tokens INTEGER,
	cost_usd REAL,
	model TEXT,
	turn_count INTEGER,
	error_text TEXT
);

CREATE TABLE IF NOT EXISTS review_comments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	issue_id INTEGER NOT NULL REFERENCES issues(id),
	github_comment_id INTEGER NOT NULL UNIQUE,
	author TEXT NOT NULL,
	body TEXT NOT NULL,
	created_at DATETIME NOT NULL,
	severity TEXT,
	topic TEXT,
	confidence REAL,
	ai_classified INTEGER DEFAULT 0,
	human_override INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS pr_complexity (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	issue_id INTEGER NOT NULL UNIQUE REFERENCES issues(id),
	lines_added INTEGER,
	lines_deleted INTEGER,
	files_changed INTEGER,
	cyclomatic_complexity_delta REAL,
	cognitive_complexity_delta REAL,
	complexity_analyzed INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS scraper_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	step TEXT NOT NULL,
	started_at DATETIME NOT NULL,
	finished_at DATETIME NOT NULL,
	status TEXT NOT NULL,
	items_processed INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS session_telemetry (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	job_run_id INTEGER NOT NULL REFERENCES job_runs(id),
	issue_key TEXT NOT NULL,
	phase TEXT NOT NULL,
	session_id TEXT,
	result TEXT,
	model TEXT,
	claude_code_version TEXT,
	prompt TEXT,
	duration_ms INTEGER,
	duration_api_ms INTEGER,
	ttft_ms INTEGER,
	num_turns INTEGER,
	total_cost_usd REAL,
	input_tokens INTEGER,
	output_tokens INTEGER,
	cache_read_input_tokens INTEGER,
	cache_creation_input_tokens INTEGER,
	cache_hit_rate_pct REAL,
	total_tool_calls INTEGER,
	tool_call_breakdown TEXT,
	skills_invoked TEXT,
	files_written INTEGER,
	num_thinking_blocks INTEGER,
	num_subagents INTEGER,
	subagent_total_tool_uses INTEGER,
	subagent_total_duration_ms INTEGER,
	is_error INTEGER DEFAULT 0,
	terminal_reason TEXT,
	stop_reason TEXT,
	analyzed_at DATETIME,
	UNIQUE(job_run_id, issue_key, phase)
);

CREATE TABLE IF NOT EXISTS otel_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	job_run_id INTEGER NOT NULL REFERENCES job_runs(id),
	session_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	timestamp_ms INTEGER,
	model TEXT,
	input_tokens INTEGER,
	output_tokens INTEGER,
	cache_read_tokens INTEGER,
	cache_creation_tokens INTEGER,
	cost_usd REAL,
	duration_ms INTEGER,
	tool_name TEXT,
	tool_success INTEGER,
	tool_input_size INTEGER,
	tool_result_size INTEGER,
	agent_type TEXT,
	total_tokens INTEGER,
	total_tool_uses INTEGER
);
CREATE INDEX IF NOT EXISTS idx_otel_events_job ON otel_events(job_run_id);
CREATE INDEX IF NOT EXISTS idx_otel_events_type ON otel_events(event_type);
`
	_, err = db.Exec(schema)
	if err != nil {
		return err
	}

	// Migrations for existing databases: add new columns if missing.
	migrations := []string{
		"ALTER TABLE issues ADD COLUMN pr_created_at DATETIME",
		"ALTER TABLE issues ADD COLUMN closed_at DATETIME",
		"ALTER TABLE review_comments ADD COLUMN confidence REAL",
		"ALTER TABLE pr_complexity ADD COLUMN complexity_analyzed INTEGER DEFAULT 0",
	}
	for _, m := range migrations {
		_, err := db.Exec(m)
		if err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("migration %q: %w", m, err)
		}
	}

	return nil
}
