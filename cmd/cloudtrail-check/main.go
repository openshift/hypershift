package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cloudtrail-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	artifactsDir := flags.String("artifacts-dir", envOr("ARTIFACT_DIR", "."), "Directory containing hypershift dump artifacts")
	startTimeValue := flags.String("start-time", os.Getenv("CLOUDTRAIL_START_TIME"), "CloudTrail query start time (RFC3339; defaults to CLOUDTRAIL_START_TIME)")
	endTimeValue := flags.String("end-time", "", "CloudTrail query end time (RFC3339; defaults to now)")
	regionValue := flags.String("region", os.Getenv("AWS_REGION"), "AWS region to query in addition to regions found in artifacts")
	outputPath := flags.String("output", "", "Report path (defaults to cloudtrail-permission-denied.json in artifacts directory)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}

	if *startTimeValue == "" {
		fmt.Fprintln(stderr, "--start-time or CLOUDTRAIL_START_TIME is required")
		return 2
	}
	startTime, err := parseTime(*startTimeValue)
	if err != nil {
		fmt.Fprintf(stderr, "invalid start time: %v\n", err)
		return 2
	}
	endTime := time.Now().UTC()
	if *endTimeValue != "" {
		endTime, err = parseTime(*endTimeValue)
		if err != nil {
			fmt.Fprintf(stderr, "invalid end time: %v\n", err)
			return 2
		}
	}
	if !endTime.After(startTime) {
		fmt.Fprintln(stderr, "end time must be after start time")
		return 2
	}

	inventory, err := scanArtifacts(*artifactsDir)
	if err != nil {
		fmt.Fprintf(stderr, "scan artifacts: %v\n", err)
		return 1
	}
	if len(inventory.RoleARNs) == 0 {
		fmt.Fprintln(stderr, "no AWS role ARNs found in artifacts")
		return 1
	}
	regions := inventory.Regions
	if *regionValue != "" {
		regions = append(regions, *regionValue)
	}
	regions = append(regions, globalServicesRegion)
	regions = uniqueSorted(regions)

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	report, queryErr := queryCloudTrail(queryCtx, startTime, endTime, regions, inventory.RoleARNs)
	if queryErr != nil {
		report.Partial = true
		report.Warnings = append(report.Warnings, "CloudTrail query incomplete; see command output for details")
	}
	if *outputPath == "" {
		*outputPath = filepath.Join(*artifactsDir, "cloudtrail-permission-denied.json")
	}
	if err := writeReport(*outputPath, report); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "CloudTrail report: %s\n", *outputPath)
	fmt.Fprintf(stdout, "Scanned %d role(s) in %d region(s); found %d permission-denied event(s).\n", len(report.RoleARNs), len(report.Regions), len(report.Events))
	if queryErr != nil {
		fmt.Fprintf(stderr, "CloudTrail query incomplete: %v\n", queryErr)
		return 1
	}
	if len(report.Events) > 0 {
		return 1
	}
	return 0
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func writeReport(path string, report cloudTrailReport) error {
	if path == "" {
		return errors.New("report path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return nil
}
