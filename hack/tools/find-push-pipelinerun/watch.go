package main

import (
	"fmt"
	"io"
	"time"
)

// WatchConfig controls watch loop behavior.
type WatchConfig struct {
	Interval  time.Duration
	SHA       string
	Component string
	Stdout    io.Writer
	Stderr    io.Writer
}

// WatchBuildPipeline polls until all push PipelineRuns reach a terminal status.
func WatchBuildPipeline(q Querier, cfg WatchConfig) error {
	for {
		prs, err := q.ListPipelineRuns(cfg.SHA)
		if err != nil {
			return err
		}
		if cfg.Component != "" {
			prs = FilterByComponent(prs, cfg.Component)
		}
		FormatPipelineRuns(cfg.Stdout, prs)
		if !HasPending(prs) {
			return nil
		}
		fmt.Fprintf(cfg.Stderr, "\n--- refreshing (%s) ---\n\n", cfg.Interval)
		time.Sleep(cfg.Interval)
	}
}

// WatchReleasePipeline polls until both PipelineRuns and Releases reach terminal status.
// When PipelineRuns are done but no Release CR exists yet (normal delay between
// Snapshot passing and Release auto-creation), it continues polling rather than exiting.
func WatchReleasePipeline(q Querier, cfg WatchConfig) error {
	for {
		fmt.Fprintf(cfg.Stderr, "\n--- refreshing (%s) ---\n\n", cfg.Interval)
		time.Sleep(cfg.Interval)

		prs, err := q.ListPipelineRuns(cfg.SHA)
		if err != nil {
			return err
		}
		if cfg.Component != "" {
			prs = FilterByComponent(prs, cfg.Component)
		}
		FormatPipelineRuns(cfg.Stdout, prs)

		if HasPending(prs) {
			continue
		}

		releases, err := q.ListReleases(cfg.SHA)
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "Warning: failed to query Releases: %v\n", err)
			continue
		}
		if len(releases) == 0 {
			continue
		}
		if cfg.Component != "" {
			releases = FilterReleasesByComponent(releases, cfg.Component)
		}

		printReleasePipelineFromReleases(q, releases, cfg.Stdout, cfg.Stderr)

		if !HasPendingReleases(releases) {
			return nil
		}
	}
}

// printReleasePipeline queries releases and displays release information.
func printReleasePipeline(q Querier, sha, component string, stdout, stderr io.Writer) {
	releases, err := q.ListReleases(sha)
	if err != nil || len(releases) == 0 {
		return
	}

	if component != "" {
		releases = FilterReleasesByComponent(releases, component)
	}

	printReleasePipelineFromReleases(q, releases, stdout, stderr)
}

// printReleasePipelineFromReleases displays release information from an already-fetched list.
func printReleasePipelineFromReleases(q Querier, releases []Release, stdout, stderr io.Writer) {
	images := ResolveDestImages(q, releases)
	fmt.Fprintln(stdout)
	FormatReleasesWithImages(stdout, releases, images)

	var allRelPRs []PipelineRun
	for _, rel := range releases {
		relPRs, err := q.ListReleasePipelineRuns(rel.Metadata.Name)
		if err != nil {
			fmt.Fprintf(stderr, "Warning: failed to query release PipelineRuns for %s: %v\n", rel.Metadata.Name, err)
			continue
		}
		allRelPRs = append(allRelPRs, relPRs...)
	}
	if len(allRelPRs) > 0 {
		fmt.Fprintln(stdout)
		FormatReleasePipelineRuns(stdout, allRelPRs)
	}
}
