package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func setupCloudTrailTest(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	sharedDir := t.TempDir()
	for _, name := range []string{"config", "credentials"} {
		if err := os.WriteFile(filepath.Join(sharedDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string]string{
		"AWS_ENDPOINT_URL_CLOUDTRAIL":         server.URL,
		"AWS_ENDPOINT_URL":                    "",
		"AWS_ACCESS_KEY_ID":                   "AKIDEXAMPLE",
		"AWS_SECRET_ACCESS_KEY":               "dummy-secret-access-key",
		"AWS_SESSION_TOKEN":                   "dummy-session-token",
		"AWS_MAX_ATTEMPTS":                    "1",
		"AWS_EC2_METADATA_DISABLED":           "true",
		"AWS_USE_FIPS_ENDPOINT":               "false",
		"AWS_USE_DUALSTACK_ENDPOINT":          "false",
		"AWS_IGNORE_CONFIGURED_ENDPOINT_URLS": "false",
		"AWS_REGION":                          "",
		"AWS_DEFAULT_REGION":                  "",
		"AWS_PROFILE":                         "",
		"AWS_DEFAULT_PROFILE":                 "",
		"AWS_CONFIG_FILE":                     filepath.Join(sharedDir, "config"),
		"AWS_SHARED_CREDENTIALS_FILE":         filepath.Join(sharedDir, "credentials"),
	} {
		t.Setenv(name, value)
	}
}

type runTestCase struct {
	name           string
	setup          string
	reply          string
	args           []string
	env            map[string]string
	manifest       string
	platformRegion string
	kmsRegion      string
	partition      string
	wantCode       int
	wantCalls      int64
	wantEvents     int
	wantReport     bool
	wantPartial    bool
	wantError      string
	wantRegions    []string
}

func TestRun(t *testing.T) {
	const (
		startValue = "2026-09-29T10:00:00Z"
		endValue   = "2026-09-29T11:00:00Z"
		roleARN    = "arn:aws:iam::123456789012:role/ControlPlaneOperator"
	)
	start := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	payload, err := json.Marshal(map[string]any{
		"eventName":   "CreateNetworkInterface",
		"eventSource": "ec2.amazonaws.com",
		"errorCode":   "AccessDenied",
		"userIdentity": map[string]any{
			"sessionContext": map[string]any{
				"sessionIssuer": map[string]any{"arn": roleARN},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	deniedEvent := map[string]any{"CloudTrailEvent": string(payload), "EventTime": start.Add(time.Minute).Unix()}
	tests := []runTestCase{
		{
			name:       "When no management-event denials are observed, it should write a provisional report and exit zero without rechecking",
			wantCalls:  1,
			wantReport: true,
		},
		{
			name:       "When management-event denials are observed, it should report them and exit one",
			reply:      "denied",
			wantCode:   1,
			wantCalls:  1,
			wantEvents: 1,
			wantReport: true,
		},
		{
			name:        "When CloudTrail rejects a query, it should write a partial report and exit one",
			reply:       "error",
			wantCode:    1,
			wantCalls:   1,
			wantReport:  true,
			wantPartial: true,
			wantError:   "AccessDeniedException",
		},
		{
			name:        "When a later page fails, it should retain earlier denials in the partial report",
			reply:       "later-page-error",
			wantCode:    1,
			wantCalls:   2,
			wantEvents:  1,
			wantReport:  true,
			wantPartial: true,
			wantError:   "AccessDeniedException",
		},
		{
			name:        "When flags are omitted, it should use environment defaults and the current end time",
			setup:       "defaults",
			env:         map[string]string{"AWS_REGION": "us-west-2"},
			wantCalls:   2,
			wantReport:  true,
			wantRegions: []string{"us-east-1", "us-west-2"},
		},
		{
			name:       "When ARTIFACT_DIR is unset, it should default to scanning the working directory",
			setup:      "working-directory",
			wantCalls:  1,
			wantReport: true,
		},
		{
			name:        "When explicit flags override invalid environment defaults, it should use the flags",
			args:        []string{"--region", "us-west-2"},
			env:         map[string]string{"ARTIFACT_DIR": "missing-artifacts", "CLOUDTRAIL_START_TIME": "invalid", "AWS_REGION": "invalid-region"},
			wantCalls:   2,
			wantReport:  true,
			wantRegions: []string{"us-east-1", "us-west-2"},
		},
		{
			name:       "When a nested output path is selected, it should create its parent directories and write the report",
			setup:      "nested-output",
			wantCalls:  1,
			wantReport: true,
		},
		{
			name:      "When an unknown flag is supplied, it should exit two without querying AWS",
			args:      []string{"--unknown"},
			wantCode:  2,
			wantError: "flag provided but not defined",
		},
		{
			name:      "When positional arguments are supplied, it should exit two without querying AWS",
			args:      []string{"unexpected"},
			wantCode:  2,
			wantError: "unexpected arguments",
		},
		{
			name:      "When no start time is supplied, it should exit two without querying AWS",
			args:      []string{"--start-time", ""},
			wantCode:  2,
			wantError: "--start-time or CLOUDTRAIL_START_TIME is required",
		},
		{
			name:      "When the start time is invalid, it should exit two without querying AWS",
			args:      []string{"--start-time", "invalid"},
			wantCode:  2,
			wantError: "invalid start time",
		},
		{
			name:      "When the end time is invalid, it should exit two without querying AWS",
			args:      []string{"--end-time", "invalid"},
			wantCode:  2,
			wantError: "invalid end time",
		},
		{
			name:      "When the end time equals the start time, it should exit two without querying AWS",
			args:      []string{"--end-time", startValue},
			wantCode:  2,
			wantError: "end time must be after start time",
		},
		{
			name:      "When the end time precedes the start time, it should exit two without querying AWS",
			args:      []string{"--end-time", "2026-09-29T09:00:00Z"},
			wantCode:  2,
			wantError: "end time must be after start time",
		},
		{
			name:      "When the artifact directory is missing, it should report a scan failure and exit one",
			setup:     "missing-artifacts",
			wantCode:  1,
			wantError: "scan artifacts:",
		},
		{
			name:      "When an artifact is malformed, it should report a scan failure and exit one",
			manifest:  "kind: [",
			wantCode:  1,
			wantError: "scan artifacts:",
		},
		{
			name:      "When artifacts contain no AWS roles, it should exit one without querying AWS",
			manifest:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: no-roles\n",
			wantCode:  1,
			wantError: "no AWS role ARNs found",
		},
		{
			name:      "When the report cannot replace a directory, it should report a write failure and exit one",
			setup:     "write-error",
			wantCode:  1,
			wantCalls: 1,
			wantError: "write report:",
		},
		{
			name:        "When a flag supplies a malicious region, it should reject it before making any AWS requests",
			args:        []string{"--region", "us-west-2@attacker.example/"},
			wantCode:    1,
			wantReport:  true,
			wantPartial: true,
			wantError:   "us-west-2@attacker.example/",
		},
		{
			name:        "When the environment supplies a malicious region, it should reject it before making any AWS requests",
			env:         map[string]string{"AWS_REGION": "us-west-2@attacker.example/"},
			wantCode:    1,
			wantReport:  true,
			wantPartial: true,
			wantError:   "us-west-2@attacker.example/",
		},
		{
			name:           "When the platform region is malicious, it should reject it before making any AWS requests",
			platformRegion: "us-west-2@attacker.example/",
			wantCode:       1,
			wantReport:     true,
			wantPartial:    true,
			wantError:      "us-west-2@attacker.example/",
		},
		{
			name:        "When the independent KMS region is malicious, it should reject it before making any AWS requests",
			kmsRegion:   "us-west-2@attacker.example/",
			wantCode:    1,
			wantReport:  true,
			wantPartial: true,
			wantError:   "us-west-2@attacker.example/",
		},
		{
			name:        "When KMS uses a different region, it should query both platform and KMS regions",
			kmsRegion:   "us-west-2",
			wantCalls:   2,
			wantReport:  true,
			wantRegions: []string{"us-east-1", "us-west-2"},
		},
		{
			name:           "When artifacts use GovCloud, it should query its global region without adding a commercial region",
			platformRegion: "us-gov-east-1",
			partition:      "aws-us-gov",
			wantCalls:      2,
			wantReport:     true,
			wantRegions:    []string{"us-gov-east-1", "us-gov-west-1"},
		},
		{
			name:           "When artifacts use China, it should query both China global regions without adding a commercial region",
			platformRegion: "cn-north-1",
			partition:      "aws-cn",
			wantCalls:      2,
			wantReport:     true,
			wantRegions:    []string{"cn-north-1", "cn-northwest-1"},
		},
		{
			name:        "When region and role partitions differ, it should reject them before making any AWS requests",
			args:        []string{"--region", "us-gov-west-1"},
			wantCode:    1,
			wantReport:  true,
			wantPartial: true,
			wantError:   "partition",
		},
		{
			name:        "When the parent context is already canceled, it should write a partial report and exit one",
			setup:       "canceled",
			wantCode:    1,
			wantReport:  true,
			wantPartial: true,
			wantError:   "canceled",
		},
		{
			name:        "When the parent context is canceled during a query, it should cancel the request and write a partial report",
			setup:       "cancel-in-flight",
			wantCode:    1,
			wantCalls:   1,
			wantReport:  true,
			wantPartial: true,
			wantError:   "canceled",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var calls atomic.Int64
			setupCloudTrailTest(t, cloudTrailResponseHandler(t, tc, deniedEvent, &calls, cancel))
			root := t.TempDir()
			partition := tc.partition
			if partition == "" {
				partition = "aws"
			}
			platformRegion := tc.platformRegion
			if platformRegion == "" {
				platformRegion = "us-east-1"
			}
			fixtureRole := fmt.Sprintf("arn:%s:iam::123456789012:role/ControlPlaneOperator", partition)
			manifest := fmt.Sprintf(`apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: cloudtrail-test
spec:
  platform:
    type: AWS
    aws:
      region: %s
      rolesRef:
        controlPlaneOperatorARN: %s
`, platformRegion, fixtureRole)
			if tc.kmsRegion != "" {
				manifest += fmt.Sprintf(`  secretEncryption:
    type: KMS
    kms:
      provider: AWS
      aws:
        region: %s
        auth:
          awsKms: %s
`, tc.kmsRegion, fixtureRole)
			}
			if tc.manifest != "" {
				manifest = tc.manifest
			}
			if err := os.WriteFile(filepath.Join(root, "hostedcluster.yaml"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("ARTIFACT_DIR", "")
			t.Setenv("CLOUDTRAIL_START_TIME", "")
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			args := []string{"--artifacts-dir", root, "--start-time", startValue, "--end-time", endValue}
			outputPath := filepath.Join(root, "cloudtrail-permission-denied.json")
			switch tc.setup {
			case "defaults":
				args = nil
				t.Setenv("ARTIFACT_DIR", root)
				t.Setenv("CLOUDTRAIL_START_TIME", startValue)
			case "working-directory":
				args = nil
				t.Chdir(root)
				t.Setenv("CLOUDTRAIL_START_TIME", startValue)
			case "missing-artifacts":
				args = append(args, "--artifacts-dir", filepath.Join(root, "missing"))
			case "nested-output":
				outputPath = filepath.Join(root, "reports", "nested", "denials.json")
				args = append(args, "--output", outputPath)
			case "write-error":
				outputPath = t.TempDir()
				args = append(args, "--output", outputPath)
			case "canceled":
				cancel()
			}
			args = append(args, tc.args...)
			var stdout, stderr bytes.Buffer
			before := time.Now().UTC()
			code := run(ctx, args, &stdout, &stderr)
			after := time.Now().UTC()
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d; stdout: %s; stderr: %s", code, tc.wantCode, stdout.String(), stderr.String())
			}
			if got := calls.Load(); got != tc.wantCalls {
				t.Errorf("CloudTrail requests = %d, want %d", got, tc.wantCalls)
			}
			if !strings.Contains(stderr.String(), tc.wantError) || (tc.wantError == "" && stderr.Len() != 0) {
				t.Errorf("stderr = %q, want error containing %q", stderr.String(), tc.wantError)
			}
			data, err := os.ReadFile(outputPath)
			if !tc.wantReport {
				if err == nil {
					t.Fatalf("unexpected report: %s", data)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			report := assertRunReport(t, tc, data, stdout.String(), outputPath)
			assertRunReportScope(t, tc, report, fixtureRole, roleARN, start, end, before, after)
		})
	}
}

func cloudTrailResponseHandler(t *testing.T, tc runTestCase, deniedEvent map[string]any, calls *atomic.Int64, cancel context.CancelFunc) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if tc.setup == "cancel-in-flight" {
			cancel()
			return
		}
		var input struct {
			NextToken string `json:"NextToken"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode CloudTrail request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		status := http.StatusOK
		response := map[string]any{"Events": []any{}}
		switch tc.reply {
		case "denied":
			response["Events"] = []any{deniedEvent}
		case "error":
			status = http.StatusBadRequest
			response = map[string]any{"__type": "AccessDeniedException", "message": "CloudTrail access denied"}
		case "later-page-error":
			if input.NextToken == "" {
				response["Events"] = []any{deniedEvent}
				response["NextToken"] = "next-page"
			} else {
				if input.NextToken != "next-page" {
					t.Errorf("unexpected pagination token: %q", input.NextToken)
				}
				status = http.StatusBadRequest
				response = map[string]any{"__type": "AccessDeniedException", "message": "CloudTrail later page denied"}
			}
		}
		data, err := json.Marshal(response)
		if err != nil {
			t.Errorf("marshal CloudTrail response: %v", err)
			http.Error(w, "invalid response", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		w.WriteHeader(status)
		if _, err := w.Write(data); err != nil {
			t.Errorf("write CloudTrail response: %v", err)
		}
	}
}

func assertRunReport(t *testing.T, tc runTestCase, data []byte, stdout, outputPath string) cloudTrailReport {
	t.Helper()
	var report cloudTrailReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != tc.wantEvents || report.Partial != tc.wantPartial {
		t.Errorf("report = %#v, want %d event(s) and partial %t", report, tc.wantEvents, tc.wantPartial)
	}
	wantProvisional := tc.wantCode == 0
	if report.Provisional != wantProvisional || bytes.Contains(data, []byte(`"provisional"`)) != wantProvisional {
		t.Errorf("provisional report = %s, want provisional %t", data, wantProvisional)
	}
	if strings.Contains(stdout, "Provisional result:") != wantProvisional {
		t.Errorf("stdout = %q, want provisional message %t", stdout, wantProvisional)
	}
	printedPath := outputPath
	if tc.setup == "working-directory" {
		printedPath = "cloudtrail-permission-denied.json"
	}
	if !strings.Contains(stdout, "CloudTrail report: "+printedPath) || !strings.Contains(stdout, fmt.Sprintf("found %d permission-denied management event(s)", tc.wantEvents)) {
		t.Errorf("unexpected report summary: %q", stdout)
	}
	warnings := strings.Join(report.Warnings, " ")
	if wantProvisional {
		for _, message := range []string{"Provisional", "eventually consistent", "settle", "recheck"} {
			if !strings.Contains(warnings, message) || !strings.Contains(stdout, message) {
				t.Errorf("missing provisional explanation %q in report warnings %q or stdout %q", message, warnings, stdout)
			}
		}
	}
	if tc.wantPartial && !strings.Contains(warnings, "incomplete") {
		t.Errorf("partial report lacks incomplete-query warning: %q", warnings)
	}
	return report
}

func assertRunReportScope(t *testing.T, tc runTestCase, report cloudTrailReport, fixtureRole, roleARN string, start, end, before, after time.Time) {
	t.Helper()
	if tc.wantCalls > 0 || tc.wantCode == 0 {
		wantRegions := tc.wantRegions
		if wantRegions == nil {
			wantRegions = []string{"us-east-1"}
		}
		if !reflect.DeepEqual(report.Regions, wantRegions) || !reflect.DeepEqual(report.RoleARNs, []string{fixtureRole}) {
			t.Errorf("reported regions/roles = %v/%v, want %v/%v", report.Regions, report.RoleARNs, wantRegions, []string{fixtureRole})
		}
		if !report.StartTime.Equal(start) {
			t.Errorf("start time = %s, want %s", report.StartTime, start)
		}
		if tc.setup == "defaults" || tc.setup == "working-directory" {
			if report.EndTime.Before(before) || report.EndTime.After(after) {
				t.Errorf("default end time = %s, want between %s and %s", report.EndTime, before, after)
			}
		} else if !report.EndTime.Equal(end) {
			t.Errorf("end time = %s, want %s", report.EndTime, end)
		}
	}
	for _, event := range report.Events {
		if event.RoleARN != roleARN || event.Region != "us-east-1" || event.EventName != "CreateNetworkInterface" || event.EventSource != "ec2.amazonaws.com" || event.ErrorCode != "AccessDenied" || !event.EventTime.Equal(start.Add(time.Minute)) {
			t.Errorf("unexpected denial: %#v", event)
		}
	}
}

func TestWriteReport(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		setup        string
		marshalError bool
		wantError    bool
	}{
		{
			name: "When the report path is new, it should write formatted JSON with the report file mode",
			path: "report.json",
		},
		{
			name: "When the report path has missing parents, it should create them and write the report",
			path: "nested/reports/report.json",
		},
		{
			name:  "When a report already exists, it should replace the old file with the complete new report",
			path:  "report.json",
			setup: "file",
		},
		{
			name:  "When the final report path is a symlink, it should replace the link and leave the outside target untouched",
			path:  "report.json",
			setup: "symlink",
		},
		{
			name:  "When the final report path is a dangling symlink, it should replace the link without creating the outside target",
			path:  "report.json",
			setup: "dangling-symlink",
		},
		{
			name:      "When the destination is a directory, it should preserve its contents and clean up the temporary file",
			path:      "report.json",
			setup:     "directory",
			wantError: true,
		},
		{
			name:      "When a parent component is a file, it should return an error and preserve that file",
			path:      "blocked/report.json",
			setup:     "blocked-parent",
			wantError: true,
		},
		{
			name:      "When the report path is empty, it should return an error without creating files",
			wantError: true,
		},
		{
			name:         "When marshaling fails, it should return an error before creating the destination parent",
			path:         "nested/report.json",
			marshalError: true,
			wantError:    true,
		},
		{
			name:         "When marshaling a replacement fails, it should preserve the old report",
			path:         "report.json",
			setup:        "file",
			marshalError: true,
			wantError:    true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := ""
			if tc.path != "" {
				path = filepath.Join(root, tc.path)
			}
			oldContent := []byte("previous contents\n")
			preservedPath := ""
			outsidePath := ""
			switch tc.setup {
			case "file":
				preservedPath = path
			case "blocked-parent":
				preservedPath = filepath.Join(root, "blocked")
			case "directory":
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
				preservedPath = filepath.Join(path, "keep.txt")
			case "symlink", "dangling-symlink":
				outsidePath = filepath.Join(t.TempDir(), "outside.json")
				if tc.setup == "symlink" {
					if err := os.WriteFile(outsidePath, oldContent, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(outsidePath, path); err != nil {
					t.Fatal(err)
				}
			}
			if preservedPath != "" {
				if err := os.WriteFile(preservedPath, oldContent, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			report := cloudTrailReport{
				StartTime: time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC),
				EndTime:   time.Date(2026, time.September, 29, 11, 0, 0, 0, time.UTC),
				Regions:   []string{"us-east-1"},
				RoleARNs:  []string{"arn:aws:iam::123456789012:role/ControlPlaneOperator"},
				Events:    []permissionDeniedEvent{},
			}
			if tc.marshalError {
				report.StartTime = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
			}
			err := writeReport(path, report)
			if (err != nil) != tc.wantError {
				t.Fatalf("writeReport error = %v, want error %t", err, tc.wantError)
			}
			if !tc.wantError {
				want, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				assertFileContentsAndMode(t, path, append(want, '\n'), 0o644)
			} else if preservedPath != "" {
				assertFileContentsAndMode(t, preservedPath, oldContent, 0o600)
			} else {
				entries, err := os.ReadDir(root)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 0 {
					t.Errorf("failed report write created files or directories: %v", entries)
				}
			}
			if outsidePath != "" {
				if tc.setup == "dangling-symlink" {
					data, err := os.ReadFile(outsidePath)
					if !os.IsNotExist(err) {
						t.Errorf("outside target was created: contents %q, error %v", data, err)
					}
				} else {
					assertFileContentsAndMode(t, outsidePath, oldContent, 0o600)
				}
			}
			if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if strings.HasPrefix(entry.Name(), ".cloudtrail-report-") {
					t.Errorf("temporary report file was not removed: %s", path)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func assertFileContentsAndMode(t *testing.T, path string, want []byte, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("file %s contents = %q, want %q", path, data, want)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		t.Errorf("file %s mode = %s, want a regular file with mode %#o", path, info.Mode(), mode)
	}
}
