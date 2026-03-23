package main

import (
	"context"
	"database/sql"
	"log"
	"os"

	"github.com/openshift/jira-agent-dashboard/internal/db"
	"github.com/openshift/jira-agent-dashboard/internal/scraper"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	log.Println("jira-agent-scraper starting...")

	// Parse environment variables
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "dashboard.db"
	}
	githubToken := os.Getenv("GITHUB_TOKEN")
	claudeAPIKey := os.Getenv("CLAUDE_API_KEY")
	claudeAPIEndpoint := os.Getenv("CLAUDE_API_ENDPOINT")
	if claudeAPIEndpoint == "" {
		claudeAPIEndpoint = "https://api.anthropic.com/v1/messages"
	}

	// Open SQLite database and initialize schema
	sqlDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer sqlDB.Close()

	if err := db.InitSchema(sqlDB); err != nil {
		log.Fatalf("Failed to initialize schema: %v", err)
	}
	log.Printf("Database initialized at %s", dbPath)

	store := db.NewStore(sqlDB)

	// Create clients
	gcsClient := scraper.NewHTTPGCSClient(nil)
	githubClient := scraper.NewGitHubClient(githubToken)
	complexityAnalyzer := scraper.NewComplexityAnalyzer(os.TempDir())
	classifier := scraper.NewClassifier(claudeAPIEndpoint, claudeAPIKey)

	// Create orchestrator and run
	orch := scraper.NewOrchestrator(store, gcsClient, githubClient, complexityAnalyzer, classifier)

	ctx := context.Background()
	if err := orch.Run(ctx); err != nil {
		log.Fatalf("Scraper failed: %v", err)
	}

	log.Println("Scraper completed successfully.")
	os.Exit(0)
}
