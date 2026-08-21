package db

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestInitSchema(t *testing.T) {
	conn, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := InitSchema(conn); err != nil {
		t.Fatalf("InitSchema failed: %v", err)
	}

	tables := []string{"job_runs", "issues", "phase_metrics", "review_comments", "pr_complexity"}
	for _, table := range tables {
		var name string
		err := conn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}
}
