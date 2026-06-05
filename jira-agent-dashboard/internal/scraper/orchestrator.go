package scraper

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/jira-agent-dashboard/internal/db"
)

// prowTimestamp represents the JSON structure of Prow's started.json/finished.json.
type prowTimestamp struct {
	Timestamp int64 `json:"timestamp"`
}

// GitHubAPI abstracts GitHub API access for dependency injection.
type GitHubAPI interface {
	GetPR(ctx context.Context, owner, repo string, number int) (*PRInfo, error)
	GetPRReviewComments(ctx context.Context, owner, repo string, number int) ([]CommentInfo, error)
}

// ComplexityAnalyzerAPI abstracts complexity analysis for dependency injection.
type ComplexityAnalyzerAPI interface {
	AnalyzePR(ctx context.Context, owner, repo string, prNumber int, baseBranch string) (*ComplexityResult, error)
}

// Orchestrator coordinates scraping GCS artifacts, refreshing GitHub PR state,
// and classifying review comments.
type Orchestrator struct {
	store      *db.Store
	gcs        GCSClient
	gh         GitHubAPI
	complexity ComplexityAnalyzerAPI
	outputPath      string
	inputPath       string
	skillConfigPath string
}

// NewOrchestrator creates an Orchestrator with the given dependencies.
func NewOrchestrator(store *db.Store, gcs GCSClient, gh GitHubAPI, ca ComplexityAnalyzerAPI) *Orchestrator {
	return &Orchestrator{
		store:      store,
		gcs:        gcs,
		gh:         gh,
		complexity: ca,
	}
}

// SetIOPaths configures file paths for export/import steps.
func (o *Orchestrator) SetIOPaths(output, input, skillConfig string) {
	o.outputPath = output
	o.inputPath = input
	o.skillConfigPath = skillConfig
}

// Run executes all scrape steps sequentially.
func (o *Orchestrator) Run(ctx context.Context) error {
	if err := o.RunStep(ctx, "all"); err != nil {
		return err
	}
	return nil
}

// RunStep executes a single scrape step by name.
// Valid steps: "prow", "github", "complexity", "all".
func (o *Orchestrator) RunStep(ctx context.Context, step string) error {
	switch step {
	case "prow":
		log.Println("Scraping new job runs from Prow CI artifacts...")
		return o.scrapeNewJobRuns(ctx)
	case "github":
		log.Println("Refreshing PR state and comments from GitHub...")
		return o.refreshGitHub(ctx)
	case "complexity":
		log.Println("Analyzing PR complexity...")
		return o.analyzeComplexity(ctx)
	case "fix-timestamps":
		log.Println("Fixing job run timestamps from GCS metadata...")
		return o.fixTimestamps(ctx)
	case "backfill-pr-dates":
		log.Println("Backfilling PR dates from GitHub for all issues...")
		return o.backfillPRDates(ctx)
	case "backfill-comments":
		log.Println("Re-fetching comments from GitHub for all issues...")
		return o.backfillComments(ctx)
	case "backfill-pr-urls":
		log.Println("Re-parsing build logs to fix missing PR URLs...")
		return o.backfillPRURLs(ctx)
	case "backfill-pr-stats":
		log.Println("Backfilling PR diff stats from GitHub for all issues...")
		return o.backfillPRStats(ctx)
	case "export-unclassified":
		log.Println("Exporting unclassified comments...")
		return o.ExportUnclassified(ctx, o.outputPath)
	case "import-classifications":
		log.Println("Importing comment classifications...")
		return o.ImportClassifications(ctx, o.inputPath)
	case "all":
		log.Println("Starting full scrape cycle...")
		if err := o.scrapeNewJobRuns(ctx); err != nil {
			return fmt.Errorf("scraping new job runs: %w", err)
		}
		if err := o.refreshGitHub(ctx); err != nil {
			return fmt.Errorf("refreshing GitHub: %w", err)
		}
		if err := o.analyzeComplexity(ctx); err != nil {
			return fmt.Errorf("analyzing complexity: %w", err)
		}
		log.Println("Scrape cycle complete.")
		return nil
	default:
		return fmt.Errorf("unknown step %q, valid steps: prow, github, complexity, all", step)
	}
}

// scrapeNewJobRuns lists builds from GCS and imports any that are not yet in the DB.
func (o *Orchestrator) scrapeNewJobRuns(ctx context.Context) error {
	builds, err := o.gcs.ListBuilds(ctx)
	if err != nil {
		return fmt.Errorf("listing builds: %w", err)
	}

	for _, buildID := range builds {
		// Check if already scraped
		_, err := o.store.GetJobRunByBuildID(buildID)
		if err == nil {
			log.Printf("Skipping already-scraped build %s", buildID)
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("checking build %s: %w", buildID, err)
		}

		// Read build-log.txt from the step directory
		data, err := o.gcs.ReadFile(ctx, buildID, "build-log.txt")
		if err != nil {
			log.Printf("Warning: could not read build-log.txt for build %s: %v", buildID, err)
			continue
		}

		result, err := ParseBuildLog(data)
		if err != nil {
			log.Printf("Warning: could not parse build-log.txt for build %s: %v", buildID, err)
			continue
		}

		// Read real start/finish times from Prow metadata
		var startedAt, finishedAt time.Time
		if data, err := o.gcs.ReadBuildFile(ctx, buildID, "started.json"); err == nil {
			var ts prowTimestamp
			if json.Unmarshal(data, &ts) == nil && ts.Timestamp > 0 {
				startedAt = time.Unix(ts.Timestamp, 0).UTC()
			}
		}
		if data, err := o.gcs.ReadBuildFile(ctx, buildID, "finished.json"); err == nil {
			var ts prowTimestamp
			if json.Unmarshal(data, &ts) == nil && ts.Timestamp > 0 {
				finishedAt = time.Unix(ts.Timestamp, 0).UTC()
			}
		}

		// Insert job_run
		jobRunID, err := o.store.InsertJobRun(&db.JobRun{
			ProwJobID:   buildID,
			BuildID:     buildID,
			StartedAt:   startedAt,
			FinishedAt:  finishedAt,
			Status:      "success",
			ArtifactURL: fmt.Sprintf("https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-hypershift-main-periodic-jira-agent/%s", buildID),
		})
		if err != nil {
			return fmt.Errorf("inserting job run %s: %w", buildID, err)
		}

		// Insert the issue
		prNumber := extractPRNumber(result.PRURL)
		issueID, err := o.store.InsertIssue(&db.Issue{
			JobRunID: jobRunID,
			JiraKey:  result.IssueKey,
			JiraURL:  fmt.Sprintf("https://issues.redhat.com/browse/%s", result.IssueKey),
			PRNumber: prNumber,
			PRURL:    result.PRURL,
			PRState:  "open",
		})
		if err != nil {
			log.Printf("Warning: could not insert issue %s: %v", result.IssueKey, err)
			continue
		}

		// Insert phase metrics from the parsed token blocks
		for _, phase := range result.Phases {
			_, err = o.store.InsertPhaseMetric(&db.PhaseMetric{
				IssueID:             issueID,
				Phase:               phase.PhaseName,
				Status:              "success",
				DurationMs:          phase.DurationMs,
				InputTokens:         phase.InputTokens,
				OutputTokens:        phase.OutputTokens,
				CacheReadTokens:     phase.CacheReadInputTokens,
				CacheCreationTokens: phase.CacheCreationInputTokens,
				CostUSD:             phase.TotalCostUSD,
				Model:               phase.Model,
				TurnCount:           phase.NumTurns,
			})
			if err != nil {
				log.Printf("Warning: could not insert phase metric for %s/%s: %v", result.IssueKey, phase.PhaseName, err)
			}
		}

		log.Printf("Scraped build %s: issue=%s, phases=%d, pr=%s", buildID, result.IssueKey, len(result.Phases), result.PRURL)
	}

	return nil
}

// issuesForRefresh returns the combined list of open issues and issues needing PR data,
// filtering out issues merged > 7 days ago.
func (o *Orchestrator) issuesForRefresh() ([]db.Issue, error) {
	open, err := o.store.GetOpenIssues()
	if err != nil {
		return nil, fmt.Errorf("getting open issues: %w", err)
	}

	needingData, err := o.store.GetIssuesNeedingPRData()
	if err != nil {
		return nil, fmt.Errorf("getting issues needing PR data: %w", err)
	}

	seen := make(map[int64]bool)
	var result []db.Issue
	for _, list := range [][]db.Issue{open, needingData} {
		for _, issue := range list {
			if seen[issue.ID] || issue.PRNumber == 0 || issue.PRURL == "" {
				continue
			}
			if issue.MergedAt != nil && time.Since(*issue.MergedAt) > 7*24*time.Hour {
				log.Printf("Skipping issue %s: merged > 7 days ago", issue.JiraKey)
				continue
			}
			seen[issue.ID] = true
			result = append(result, issue)
		}
	}
	return result, nil
}

// issuesForComplexity returns issues that need complexity analysis:
// either they have no pr_complexity row yet, or their complexity deltas are 0.
func (o *Orchestrator) issuesForComplexity() ([]db.Issue, error) {
	needingData, err := o.store.GetIssuesNeedingComplexity()
	if err != nil {
		return nil, fmt.Errorf("getting issues needing complexity: %w", err)
	}

	// Also include open issues that might need re-analysis
	refresh, err := o.issuesForRefresh()
	if err != nil {
		return nil, err
	}

	seen := make(map[int64]bool)
	var result []db.Issue
	for _, list := range [][]db.Issue{needingData, refresh} {
		for _, issue := range list {
			if seen[issue.ID] || issue.PRNumber == 0 || issue.PRURL == "" {
				continue
			}
			seen[issue.ID] = true
			result = append(result, issue)
		}
	}
	return result, nil
}

// refreshGitHub fetches PR state, diff stats, and review comments from GitHub.
func (o *Orchestrator) refreshGitHub(ctx context.Context) error {
	issues, err := o.issuesForRefresh()
	if err != nil {
		return err
	}

	for _, issue := range issues {
		owner, repo := extractOwnerRepo(issue.PRURL)
		if owner == "" || repo == "" {
			log.Printf("Warning: could not parse owner/repo from %s", issue.PRURL)
			continue
		}

		prInfo, err := o.gh.GetPR(ctx, owner, repo, issue.PRNumber)
		if err != nil {
			log.Printf("Warning: could not fetch PR %d: %v", issue.PRNumber, err)
			continue
		}

		prState := prInfo.State
		if prInfo.Merged {
			prState = "merged"
		}

		var mergeDuration *float64
		if prInfo.MergedAt != nil && prInfo.CreatedAt != nil {
			hours := prInfo.MergedAt.Sub(*prInfo.CreatedAt).Hours()
			if hours < 0 {
				hours = 0
			}
			mergeDuration = &hours
		}

		if err := o.store.UpdateIssueState(issue.ID, prState, prInfo.CreatedAt, prInfo.MergedAt, prInfo.ClosedAt, mergeDuration); err != nil {
			log.Printf("Warning: could not update issue %s state: %v", issue.JiraKey, err)
			continue
		}

		// Store diff stats (lines added/deleted, files changed) without complexity
		if err := o.store.InsertOrUpdatePRComplexity(&db.PRComplexity{
			IssueID:      issue.ID,
			LinesAdded:   prInfo.Additions,
			LinesDeleted: prInfo.Deletions,
			FilesChanged: prInfo.ChangedFiles,
		}); err != nil {
			log.Printf("Warning: could not upsert PR stats for issue %s: %v", issue.JiraKey, err)
		}

		// Fetch and insert review comments (skip noise)
		comments, err := o.gh.GetPRReviewComments(ctx, owner, repo, issue.PRNumber)
		if err != nil {
			log.Printf("Warning: could not fetch comments for PR %d: %v", issue.PRNumber, err)
		} else {
			for _, c := range comments {
				if IsNoiseComment(c.Author, c.Body) {
					continue
				}
				_, err := o.store.InsertReviewComment(&db.ReviewComment{
					IssueID:         issue.ID,
					GitHubCommentID: c.ID,
					Author:          c.Author,
					Body:            c.Body,
					CreatedAt:       c.CreatedAt,
				})
				if err != nil {
					if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
						log.Printf("Warning: could not insert comment %d: %v", c.ID, err)
					}
				}
			}
		}

		log.Printf("Refreshed PR %d for issue %s: state=%s, +%d/-%d lines, %d files",
			issue.PRNumber, issue.JiraKey, prState, prInfo.Additions, prInfo.Deletions, prInfo.ChangedFiles)
	}

	return nil
}

// analyzeComplexity runs gocyclo/gocognit on PRs that need complexity analysis.
func (o *Orchestrator) analyzeComplexity(ctx context.Context) error {
	issues, err := o.issuesForComplexity()
	if err != nil {
		return err
	}

	for _, issue := range issues {
		owner, repo := extractOwnerRepo(issue.PRURL)
		if owner == "" || repo == "" {
			continue
		}

		result, err := o.complexity.AnalyzePR(ctx, owner, repo, issue.PRNumber, "main")
		if err != nil {
			log.Printf("Warning: could not analyze complexity for PR %d: %v", issue.PRNumber, err)
			continue
		}

		// Update only the complexity deltas (diff stats already populated by github step).
		// If the row doesn't exist yet (standalone complexity step), use zero diff stats.
		existing, err := o.store.GetPRComplexityByIssueID(issue.ID)
		if err != nil && err != sql.ErrNoRows {
			log.Printf("Warning: could not get existing PR complexity for issue %s: %v", issue.JiraKey, err)
			continue
		}
		if existing == nil {
			existing = &db.PRComplexity{}
		}

		if err := o.store.InsertOrUpdatePRComplexity(&db.PRComplexity{
			IssueID:                   issue.ID,
			LinesAdded:                existing.LinesAdded,
			LinesDeleted:              existing.LinesDeleted,
			FilesChanged:              existing.FilesChanged,
			CyclomaticComplexityDelta: result.CyclomaticDelta,
			CognitiveComplexityDelta:  result.CognitiveDelta,
			ComplexityAnalyzed:        true,
		}); err != nil {
			log.Printf("Warning: could not update complexity for issue %s: %v", issue.JiraKey, err)
			continue
		}

		log.Printf("Analyzed complexity for PR %d (%s): cyclomatic=%.1f, cognitive=%.1f",
			issue.PRNumber, issue.JiraKey, result.CyclomaticDelta, result.CognitiveDelta)
	}

	return nil
}

// fixTimestamps re-reads started.json/finished.json from GCS for all existing
// job runs and updates their timestamps. This is a one-time fix for runs that
// were initially scraped with time.Now() instead of actual Prow timestamps.
func (o *Orchestrator) fixTimestamps(ctx context.Context) error {
	runs, err := o.store.ListAllJobRuns()
	if err != nil {
		return fmt.Errorf("listing job runs: %w", err)
	}

	fixed := 0
	for _, run := range runs {
		var startedAt, finishedAt time.Time
		updated := false

		if data, err := o.gcs.ReadBuildFile(ctx, run.BuildID, "started.json"); err == nil {
			var ts prowTimestamp
			if json.Unmarshal(data, &ts) == nil && ts.Timestamp > 0 {
				startedAt = time.Unix(ts.Timestamp, 0).UTC()
				updated = true
			}
		}
		if data, err := o.gcs.ReadBuildFile(ctx, run.BuildID, "finished.json"); err == nil {
			var ts prowTimestamp
			if json.Unmarshal(data, &ts) == nil && ts.Timestamp > 0 {
				finishedAt = time.Unix(ts.Timestamp, 0).UTC()
				updated = true
			}
		}

		if updated {
			if startedAt.IsZero() {
				startedAt = run.StartedAt
			}
			if finishedAt.IsZero() {
				finishedAt = run.FinishedAt
			}
			if err := o.store.UpdateJobRunTimestamps(run.ID, startedAt, finishedAt); err != nil {
				log.Printf("Warning: could not update timestamps for build %s: %v", run.BuildID, err)
				continue
			}
			log.Printf("Fixed timestamps for build %s: started=%s, finished=%s", run.BuildID, startedAt, finishedAt)
			fixed++
		}
	}

	log.Printf("Fixed timestamps for %d/%d job runs", fixed, len(runs))
	return nil
}

// backfillPRDates fetches PR dates from GitHub for all issues, regardless of
// merge age. This is a one-time operation to populate pr_created_at for issues
// that were skipped by the normal refresh (which has a 7-day cutoff).
func (o *Orchestrator) backfillPRDates(ctx context.Context) error {
	issues, err := o.store.GetAllIssuesWithPR()
	if err != nil {
		return fmt.Errorf("getting all issues with PR: %w", err)
	}

	updated := 0
	for _, issue := range issues {
		owner, repo := extractOwnerRepo(issue.PRURL)
		if owner == "" || repo == "" {
			continue
		}

		prInfo, err := o.gh.GetPR(ctx, owner, repo, issue.PRNumber)
		if err != nil {
			log.Printf("Warning: could not fetch PR %d: %v", issue.PRNumber, err)
			continue
		}

		prState := prInfo.State
		if prInfo.Merged {
			prState = "merged"
		}

		var mergeDuration *float64
		if prInfo.MergedAt != nil && prInfo.CreatedAt != nil {
			hours := prInfo.MergedAt.Sub(*prInfo.CreatedAt).Hours()
			if hours < 0 {
				hours = 0
			}
			mergeDuration = &hours
		}

		if err := o.store.UpdateIssueState(issue.ID, prState, prInfo.CreatedAt, prInfo.MergedAt, prInfo.ClosedAt, mergeDuration); err != nil {
			log.Printf("Warning: could not update issue %s: %v", issue.JiraKey, err)
			continue
		}

		log.Printf("Backfilled PR dates for %s (PR #%d): state=%s, created=%v",
			issue.JiraKey, issue.PRNumber, prState, prInfo.CreatedAt)
		updated++
	}

	log.Printf("Backfilled PR dates for %d/%d issues", updated, len(issues))
	return nil
}

// backfillPRURLs re-parses build logs for issues with pr_number=0 to pick up
// PR URLs that were missed by the original parser (e.g. "PR:" vs "PR created:" format).
func (o *Orchestrator) backfillPRURLs(ctx context.Context) error {
	issues, err := o.store.GetIssuesMissingPR()
	if err != nil {
		return fmt.Errorf("getting issues missing PR: %w", err)
	}

	fixed := 0
	for _, issue := range issues {
		data, err := o.gcs.ReadFile(ctx, issue.BuildID, "build-log.txt")
		if err != nil {
			log.Printf("Warning: could not read build-log.txt for build %s: %v", issue.BuildID, err)
			continue
		}

		result, err := ParseBuildLog(data)
		if err != nil {
			log.Printf("Warning: could not parse build-log.txt for build %s: %v", issue.BuildID, err)
			continue
		}

		if result.PRURL == "" {
			log.Printf("No PR URL found in build log for %s (build %s)", issue.JiraKey, issue.BuildID)
			continue
		}

		prNumber := extractPRNumber(result.PRURL)
		if prNumber == 0 {
			continue
		}

		if err := o.store.UpdateIssuePR(issue.IssueID, prNumber, result.PRURL); err != nil {
			log.Printf("Warning: could not update PR for issue %s: %v", issue.JiraKey, err)
			continue
		}

		log.Printf("Fixed PR for %s: %s (PR #%d)", issue.JiraKey, result.PRURL, prNumber)
		fixed++
	}

	log.Printf("Fixed PR URLs for %d/%d issues", fixed, len(issues))
	return nil
}

// backfillPRStats fetches diff stats (lines added/deleted, files changed) from
// GitHub for all issues with PRs. This picks up stats for PRs that were merged
// before the normal refresh window.
func (o *Orchestrator) backfillPRStats(ctx context.Context) error {
	issues, err := o.store.GetAllIssuesWithPR()
	if err != nil {
		return fmt.Errorf("getting all issues with PR: %w", err)
	}

	updated := 0
	for _, issue := range issues {
		owner, repo := extractOwnerRepo(issue.PRURL)
		if owner == "" || repo == "" {
			continue
		}

		prInfo, err := o.gh.GetPR(ctx, owner, repo, issue.PRNumber)
		if err != nil {
			log.Printf("Warning: could not fetch PR %d: %v", issue.PRNumber, err)
			continue
		}

		if err := o.store.InsertOrUpdatePRComplexity(&db.PRComplexity{
			IssueID:      issue.ID,
			LinesAdded:   prInfo.Additions,
			LinesDeleted: prInfo.Deletions,
			FilesChanged: prInfo.ChangedFiles,
		}); err != nil {
			log.Printf("Warning: could not upsert PR stats for issue %s: %v", issue.JiraKey, err)
			continue
		}

		log.Printf("Backfilled PR stats for %s (PR #%d): +%d/-%d lines, %d files",
			issue.JiraKey, issue.PRNumber, prInfo.Additions, prInfo.Deletions, prInfo.ChangedFiles)
		updated++
	}

	log.Printf("Backfilled PR stats for %d/%d issues", updated, len(issues))
	return nil
}

// backfillComments re-fetches comments (both review and issue comments) from
// GitHub for all issues with PRs. This is a one-time operation to pick up
// issue-level comments that were previously missed.
func (o *Orchestrator) backfillComments(ctx context.Context) error {
	issues, err := o.store.GetAllIssuesWithPR()
	if err != nil {
		return fmt.Errorf("getting all issues with PR: %w", err)
	}

	updated := 0
	for _, issue := range issues {
		owner, repo := extractOwnerRepo(issue.PRURL)
		if owner == "" || repo == "" {
			continue
		}

		comments, err := o.gh.GetPRReviewComments(ctx, owner, repo, issue.PRNumber)
		if err != nil {
			log.Printf("Warning: could not fetch comments for PR %d: %v", issue.PRNumber, err)
			continue
		}

		inserted := 0
		for _, c := range comments {
			if IsNoiseComment(c.Author, c.Body) {
				continue
			}
			_, err := o.store.InsertReviewComment(&db.ReviewComment{
				IssueID:         issue.ID,
				GitHubCommentID: c.ID,
				Author:          c.Author,
				Body:            c.Body,
				CreatedAt:       c.CreatedAt,
			})
			if err != nil {
				if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
					log.Printf("Warning: could not insert comment %d: %v", c.ID, err)
				}
				continue
			}
			inserted++
		}

		if inserted > 0 {
			log.Printf("Backfilled %d new comments for %s (PR #%d)", inserted, issue.JiraKey, issue.PRNumber)
			updated++
		}
	}

	log.Printf("Backfilled comments for %d/%d issues", updated, len(issues))
	return nil
}

// refreshOpenPRs is kept for backward compatibility with tests.
func (o *Orchestrator) refreshOpenPRs(ctx context.Context) error {
	if err := o.refreshGitHub(ctx); err != nil {
		return err
	}
	return o.analyzeComplexity(ctx)
}

// prURLRe matches GitHub PR URLs like https://github.com/owner/repo/pull/123
var prURLRe = regexp.MustCompile(`https://github\.com/([^/]+)/([^/]+)/pull/(\d+)`)

// extractPRNumber parses a PR number from a GitHub PR URL.
func extractPRNumber(prURL string) int {
	matches := prURLRe.FindStringSubmatch(prURL)
	if len(matches) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(matches[3])
	return n
}

// extractOwnerRepo parses owner and repo from a GitHub PR URL.
func extractOwnerRepo(prURL string) (string, string) {
	matches := prURLRe.FindStringSubmatch(prURL)
	if len(matches) < 4 {
		return "", ""
	}
	return matches[1], matches[2]
}

// ExportComment is the JSON structure written by export-unclassified.
type ExportComment struct {
	ID     int64  `json:"id"`
	Author string `json:"author"`
	Body   string `json:"body"`
	PRURL  string `json:"pr_url,omitempty"`
}

// ClassificationResult is the JSON structure expected by import-classifications.
type ClassificationResult struct {
	ID         int64    `json:"id"`
	Severity   string   `json:"severity"`
	Topic      string   `json:"topic"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// skillConfig represents the classify-review-comment config.json structure.
type skillConfig struct {
	Severity []struct {
		Value string `json:"value"`
	} `json:"severity"`
	Topic []struct {
		Value string `json:"value"`
	} `json:"topic"`
}

func loadAllowedLabels(configPath string) (severities, topics map[string]bool, err error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading skill config %s: %w", configPath, err)
	}
	var cfg skillConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, nil, fmt.Errorf("parsing skill config: %w", err)
	}
	severities = make(map[string]bool, len(cfg.Severity))
	for _, s := range cfg.Severity {
		severities[s.Value] = true
	}
	topics = make(map[string]bool, len(cfg.Topic))
	for _, t := range cfg.Topic {
		topics[t.Value] = true
	}
	return severities, topics, nil
}

// ExportUnclassified writes unclassified comments to a JSON file.
func (o *Orchestrator) ExportUnclassified(_ context.Context, outputPath string) error {
	if outputPath == "" {
		return fmt.Errorf("--output is required for export-unclassified")
	}

	comments, err := o.store.GetUnclassifiedCommentsWithContext()
	if err != nil {
		return fmt.Errorf("querying unclassified comments: %w", err)
	}

	var exports []ExportComment
	for _, c := range comments {
		exports = append(exports, ExportComment{
			ID:     c.ID,
			Author: c.Author,
			Body:   c.Body,
			PRURL:  c.PRURL,
		})
	}

	data, err := json.MarshalIndent(exports, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling comments: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", outputPath, err)
	}

	log.Printf("Exported %d unclassified comments to %s", len(exports), outputPath)
	return nil
}

// ImportClassifications reads classification results from a JSON file and updates the DB.
// When skillConfigPath is set, it validates severity/topic values against the skill's config.json.
func (o *Orchestrator) ImportClassifications(_ context.Context, inputPath string) error {
	if inputPath == "" {
		return fmt.Errorf("--input is required for import-classifications")
	}

	var validSeverities, validTopics map[string]bool
	if o.skillConfigPath != "" {
		var err error
		validSeverities, validTopics, err = loadAllowedLabels(o.skillConfigPath)
		if err != nil {
			return fmt.Errorf("loading skill config for validation: %w", err)
		}
		log.Printf("Loaded %d severity and %d topic labels from %s", len(validSeverities), len(validTopics), o.skillConfigPath)
	}

	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", inputPath, err)
	}

	var results []ClassificationResult
	if err := json.Unmarshal(data, &results); err != nil {
		return fmt.Errorf("parsing classifications: %w", err)
	}

	updated, rejected := 0, 0
	for _, r := range results {
		if r.Severity == "" && r.Topic == "" {
			continue
		}
		if validSeverities != nil && !validSeverities[r.Severity] {
			log.Printf("Rejected comment %d: invalid severity %q", r.ID, r.Severity)
			rejected++
			continue
		}
		if validTopics != nil && !validTopics[r.Topic] {
			log.Printf("Rejected comment %d: invalid topic %q", r.ID, r.Topic)
			rejected++
			continue
		}
		if err := o.store.UpdateCommentAIClassification(r.ID, r.Severity, r.Topic, r.Confidence); err != nil {
			log.Printf("Warning: could not update comment %d: %v", r.ID, err)
			continue
		}
		updated++
	}

	if rejected > 0 {
		log.Printf("Rejected %d classifications with invalid labels", rejected)
	}
	log.Printf("Imported %d/%d classifications from %s", updated, len(results), inputPath)
	return nil
}
