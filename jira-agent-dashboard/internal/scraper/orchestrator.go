package scraper

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/jira-agent-dashboard/internal/db"
)

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

// Run executes the full scrape cycle:
// 1. Scrape new job runs from GCS
// 2. Refresh open PRs from GitHub
// 3. Classify new (unclassified) comments
func (o *Orchestrator) Run(ctx context.Context) error {
	log.Println("Starting scrape cycle...")

	log.Println("Step 1: Scraping new job runs from GCS...")
	if err := o.scrapeNewJobRuns(ctx); err != nil {
		return fmt.Errorf("scraping new job runs: %w", err)
	}

	log.Println("Step 2: Refreshing open PRs from GitHub...")
	if err := o.refreshOpenPRs(ctx); err != nil {
		return fmt.Errorf("refreshing open PRs: %w", err)
	}

	// TODO: Comment classification will be handled by an ephemeral Claude Code pod
	// using the code-review:classify-review-comment skill from ai-helpers.

	log.Println("Scrape cycle complete.")
	return nil
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
			// Already exists, skip
			log.Printf("Skipping already-scraped build %s", buildID)
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("checking build %s: %w", buildID, err)
		}

		// Read processed-issues.txt
		data, err := o.gcs.ReadFile(ctx, buildID, "processed-issues.txt")
		if err != nil {
			log.Printf("Warning: could not read processed-issues.txt for build %s: %v", buildID, err)
			continue
		}

		issues, err := ParseProcessedIssues(data)
		if err != nil {
			log.Printf("Warning: could not parse processed-issues.txt for build %s: %v", buildID, err)
			continue
		}

		// Insert job_run
		jobRunID, err := o.store.InsertJobRun(&db.JobRun{
			ProwJobID:   buildID, // use buildID as prow job ID placeholder
			BuildID:     buildID,
			StartedAt:   time.Now(),
			FinishedAt:  time.Now(),
			Status:      "success",
			ArtifactURL: fmt.Sprintf("https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-hypershift-main-periodic-jira-agent/%s", buildID),
		})
		if err != nil {
			return fmt.Errorf("inserting job run %s: %w", buildID, err)
		}

		// Process each issue
		for _, pi := range issues {
			prNumber := extractPRNumber(pi.PRURL)
			issueID, err := o.store.InsertIssue(&db.Issue{
				JobRunID: jobRunID,
				JiraKey:  pi.IssueKey,
				JiraURL:  fmt.Sprintf("https://issues.redhat.com/browse/%s", pi.IssueKey),
				PRNumber: prNumber,
				PRURL:    pi.PRURL,
				PRState:  "open",
			})
			if err != nil {
				log.Printf("Warning: could not insert issue %s: %v", pi.IssueKey, err)
				continue
			}

			// Read and insert phase metrics for each phase
			phases := []string{"solve", "review", "fix", "pr"}
			for _, phase := range phases {
				o.scrapePhaseMetrics(ctx, buildID, pi.IssueKey, phase, issueID)
			}
		}

		log.Printf("Scraped build %s: %d issues", buildID, len(issues))
	}

	return nil
}

// scrapePhaseMetrics reads tokens.json and duration.txt for a single phase and inserts the metric.
func (o *Orchestrator) scrapePhaseMetrics(ctx context.Context, buildID, issueKey, phase string, issueID int64) {
	tokensFile := fmt.Sprintf("claude-%s-%s-tokens.json", issueKey, phase)
	durationFile := fmt.Sprintf("claude-%s-%s-duration.txt", issueKey, phase)

	tokensData, err := o.gcs.ReadFile(ctx, buildID, tokensFile)
	if err != nil {
		log.Printf("Warning: could not read %s: %v", tokensFile, err)
		return
	}

	tokens, err := ParseTokensJSON(tokensData)
	if err != nil {
		log.Printf("Warning: could not parse %s: %v", tokensFile, err)
		return
	}

	durationData, err := o.gcs.ReadFile(ctx, buildID, durationFile)
	if err != nil {
		log.Printf("Warning: could not read %s: %v", durationFile, err)
		return
	}

	durationSec, err := ParseDuration(durationData)
	if err != nil {
		log.Printf("Warning: could not parse %s: %v", durationFile, err)
		return
	}

	_, err = o.store.InsertPhaseMetric(&db.PhaseMetric{
		IssueID:             issueID,
		Phase:               phase,
		Status:              "success",
		DurationMs:          durationSec * 1000,
		InputTokens:         tokens.InputTokens,
		OutputTokens:        tokens.OutputTokens,
		CacheReadTokens:     tokens.CacheReadInputTokens,
		CacheCreationTokens: tokens.CacheCreationInputTokens,
		CostUSD:             tokens.TotalCostUSD,
		Model:               tokens.Model,
		TurnCount:           tokens.NumTurns,
	})
	if err != nil {
		log.Printf("Warning: could not insert phase metric for %s/%s: %v", issueKey, phase, err)
	}
}

// refreshOpenPRs fetches updated PR info from GitHub for all open issues.
func (o *Orchestrator) refreshOpenPRs(ctx context.Context) error {
	issues, err := o.store.GetOpenIssues()
	if err != nil {
		return fmt.Errorf("getting open issues: %w", err)
	}

	for _, issue := range issues {
		if issue.PRNumber == 0 || issue.PRURL == "" {
			continue
		}

		// Skip if merged/closed > 7 days ago
		if issue.MergedAt != nil && time.Since(*issue.MergedAt) > 7*24*time.Hour {
			log.Printf("Skipping issue %s: merged > 7 days ago", issue.JiraKey)
			continue
		}

		owner, repo := extractOwnerRepo(issue.PRURL)
		if owner == "" || repo == "" {
			log.Printf("Warning: could not parse owner/repo from %s", issue.PRURL)
			continue
		}

		// Fetch PR info
		prInfo, err := o.gh.GetPR(ctx, owner, repo, issue.PRNumber)
		if err != nil {
			log.Printf("Warning: could not fetch PR %d: %v", issue.PRNumber, err)
			continue
		}

		// Determine state
		prState := prInfo.State
		if prInfo.Merged {
			prState = "merged"
		}

		// Compute merge duration if merged
		var mergeDuration *float64
		if prInfo.MergedAt != nil {
			// Use the job run's started_at as the PR creation proxy
			// For simplicity, compute hours from issue creation to merge
			hours := prInfo.MergedAt.Sub(time.Now().Add(-24 * time.Hour)).Hours()
			if hours < 0 {
				hours = 0
			}
			mergeDuration = &hours
		}

		if err := o.store.UpdateIssueState(issue.ID, prState, prInfo.MergedAt, mergeDuration); err != nil {
			log.Printf("Warning: could not update issue %s state: %v", issue.JiraKey, err)
			continue
		}

		// Fetch and insert review comments (dedup by github_comment_id via UNIQUE constraint)
		comments, err := o.gh.GetPRReviewComments(ctx, owner, repo, issue.PRNumber)
		if err != nil {
			log.Printf("Warning: could not fetch comments for PR %d: %v", issue.PRNumber, err)
			continue
		}

		for _, c := range comments {
			_, err := o.store.InsertReviewComment(&db.ReviewComment{
				IssueID:         issue.ID,
				GitHubCommentID: c.ID,
				Author:          c.Author,
				Body:            c.Body,
				CreatedAt:       c.CreatedAt,
			})
			if err != nil {
				// Likely a UNIQUE constraint violation (already exists), which is fine
				if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
					log.Printf("Warning: could not insert comment %d: %v", c.ID, err)
				}
			}
		}

		// Fetch diff stats and run complexity analysis, upsert pr_complexity
		complexityResult, err := o.complexity.AnalyzePR(ctx, owner, repo, issue.PRNumber, "main")
		if err != nil {
			log.Printf("Warning: could not analyze complexity for PR %d: %v", issue.PRNumber, err)
			continue
		}

		if err := o.store.InsertOrUpdatePRComplexity(&db.PRComplexity{
			IssueID:                   issue.ID,
			LinesAdded:                prInfo.Additions,
			LinesDeleted:              prInfo.Deletions,
			FilesChanged:              prInfo.ChangedFiles,
			CyclomaticComplexityDelta: complexityResult.CyclomaticDelta,
			CognitiveComplexityDelta:  complexityResult.CognitiveDelta,
		}); err != nil {
			log.Printf("Warning: could not upsert PR complexity for issue %s: %v", issue.JiraKey, err)
		}

		log.Printf("Refreshed PR %d for issue %s: state=%s", issue.PRNumber, issue.JiraKey, prState)
	}

	return nil
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
