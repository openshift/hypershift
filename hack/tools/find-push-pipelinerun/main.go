package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultKonfluxNamespace = "crt-redhat-acm-tenant"
	defaultRelengNamespace  = "rhtap-releng-tenant"
	defaultKAHost           = "https://kubearchive-api-server-product-kubearchive.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com"
	defaultWatchInterval    = 15 * time.Second
)

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: find-push-pipelinerun [OPTIONS] PR [COMPONENT]

Find on-push Konflux PipelineRuns triggered by a merged PR's commit SHA.
Falls back to KubeArchive when PipelineRuns have been archived.

Arguments:
  PR          GitHub URL, owner/repo#number, or bare PR number
  COMPONENT   Optional component name prefix filter

Options:
  -w, --watch     Poll until all PipelineRuns complete
  -r, --release   Show release pipeline status and destination images
  -h, --help      Show this help

Environment variables:
  KONFLUX_NAMESPACE   Konflux tenant namespace (default: %s)
  RELENG_NAMESPACE    Release engineering namespace (default: %s)
  KUBEARCHIVE_HOST    KubeArchive API host (default: %s)
  WATCH_INTERVAL      Seconds between poll cycles (default: %d)
`, defaultKonfluxNamespace, defaultRelengNamespace, defaultKAHost, int(defaultWatchInterval.Seconds()))
}

func main() {
	var watch, release bool
	flag.BoolVar(&watch, "watch", false, "Poll until all PipelineRuns complete")
	flag.BoolVar(&watch, "w", false, "Poll until all PipelineRuns complete")
	flag.BoolVar(&release, "release", false, "Show release pipeline status and destination images")
	flag.BoolVar(&release, "r", false, "Show release pipeline status and destination images")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}

	prInput := args[0]
	component := ""
	if len(args) > 1 {
		component = args[1]
	}

	if err := run(prInput, component, watch, release); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(prInput, component string, watch, release bool) error {
	ref, err := ResolvePR(prInput)
	if err != nil {
		return err
	}

	ghClient := newGitHubClient(os.Getenv("GITHUB_TOKEN"))
	sha, err := GetMergeSHA(ghClient, ref)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "PR https://github.com/%s/pull/%d merged at commit %s\n\n", ref.Repo, ref.Number, sha)

	q, err := newHTTPQuerier(loadConfig(), os.Stderr)
	if err != nil {
		return fmt.Errorf("building API client: %w", err)
	}

	prs, err := q.ListPipelineRuns(sha)
	if err != nil {
		return err
	}
	if component != "" {
		prs = FilterByComponent(prs, component)
	}
	if len(prs) == 0 {
		fmt.Fprintf(os.Stderr, "No push PipelineRuns found for commit %s\n", sha)
		return nil
	}
	FormatPipelineRuns(os.Stdout, prs)

	if watch && !release {
		return WatchBuildPipeline(q, WatchConfig{
			Interval:  watchInterval(),
			SHA:       sha,
			Component: component,
			Stdout:    os.Stdout,
			Stderr:    os.Stderr,
		})
	}

	if release {
		printReleasePipeline(q, sha, component, os.Stdout, os.Stderr)

		if watch {
			return WatchReleasePipeline(q, WatchConfig{
				Interval:  watchInterval(),
				SHA:       sha,
				Component: component,
				Stdout:    os.Stdout,
				Stderr:    os.Stderr,
			})
		}
	}

	return nil
}

func loadConfig() AppConfig {
	return AppConfig{
		KonfluxNamespace: envOrDefault("KONFLUX_NAMESPACE", defaultKonfluxNamespace),
		RelengNamespace:  envOrDefault("RELENG_NAMESPACE", defaultRelengNamespace),
		KAHost:           envOrDefault("KUBEARCHIVE_HOST", defaultKAHost),
	}
}

func watchInterval() time.Duration {
	if s := os.Getenv("WATCH_INTERVAL"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultWatchInterval
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
