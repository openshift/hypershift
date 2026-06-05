package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"

	"github.com/openshift/jira-agent-dashboard/internal/db"
	"github.com/openshift/jira-agent-dashboard/internal/scraper"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	step := flag.String("step", "all", "Scrape step to run: prow, github, complexity, or all")
	flag.Parse()

	log.Printf("jira-agent-scraper starting (step=%s)...", *step)

	// Parse environment variables
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "dashboard.db"
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

	// Create GitHub client — prefer App auth, fall back to token
	var githubClient *scraper.GitHubClient
	appID := os.Getenv("GITHUB_APP_ID")
	installationID := os.Getenv("GITHUB_APP_INSTALLATION_ID")
	privateKeyPath := os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH")

	if appID != "" && installationID != "" && privateKeyPath != "" {
		keyData, err := os.ReadFile(privateKeyPath)
		if err != nil {
			log.Fatalf("Failed to read GitHub App private key from %s: %v", privateKeyPath, err)
		}
		appAuth, err := scraper.NewGitHubAppAuth(appID, installationID, keyData)
		if err != nil {
			log.Fatalf("Failed to initialize GitHub App auth: %v", err)
		}
		githubClient = scraper.NewGitHubClientWithApp(appAuth)
		log.Printf("Using GitHub App authentication (app_id=%s, installation_id=%s)", appID, installationID)
	} else if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		githubClient = scraper.NewGitHubClient(token)
		log.Println("Using GitHub token authentication")
	} else {
		githubClient = scraper.NewGitHubClient("")
		log.Println("Warning: No GitHub authentication configured, API rate limits will be very low")
	}

	// Create remaining clients
	gcsClient := scraper.NewHTTPGCSClient(nil)
	complexityAnalyzer := scraper.NewComplexityAnalyzer(os.TempDir())

	// Create orchestrator and run
	orch := scraper.NewOrchestrator(store, gcsClient, githubClient, complexityAnalyzer)

	ctx := context.Background()
	if err := orch.RunStep(ctx, *step); err != nil {
		log.Fatalf("Scraper failed: %v", err)
	}

	log.Printf("Scraper completed successfully (step=%s).", *step)
	os.Exit(0)
}
