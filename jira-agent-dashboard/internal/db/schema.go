package db

import "database/sql"

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
	merged_at DATETIME,
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
	cognitive_complexity_delta REAL
);
`
	_, err = db.Exec(schema)
	return err
}
