//go:build e2ev2

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	clusterdump "github.com/openshift/hypershift/cmd/cluster/dump"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestCloseDiagnosticLog(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		closed bool
	}{
		{name: "When the diagnostic log is writable, it should flush and close the file"},
		{name: "When the diagnostic log is already closed, it should retain both flush and close errors", closed: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			file, err := os.Create(filepath.Join(t.TempDir(), "dump.log"))
			if err != nil {
				t.Fatal(err)
			}
			logger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.Lock(file), zap.DebugLevel))
			logger.Info("diagnostic content")
			if testCase.closed {
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = closeDiagnosticLog(file, logger)
			if testCase.closed {
				if !errors.Is(err, os.ErrClosed) || !strings.Contains(err.Error(), "flush diagnostic log") || !strings.Contains(err.Error(), "close diagnostic log") {
					t.Fatalf("flush and close errors not preserved: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString("after close"); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("diagnostic log was not closed: %v", err)
			}
			data, err := os.ReadFile(file.Name())
			if err != nil || !bytes.Contains(data, []byte("diagnostic content")) {
				t.Fatalf("diagnostic content lost: %q error=%v", data, err)
			}
		})
	}
}

func TestDestroyClusterWithDiagnostics(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		platform lifecycle.PlatformConfig
		aws      bool
	}{
		{name: "When AWS destroy needs a retry, it should collect AWS and hosted cluster diagnostics only initially", platform: &lifecycle.AWSPlatformConfig{}, aws: true},
		{name: "When Azure destroy needs a retry, it should never invoke AWS collectors", platform: &lifecycle.AzurePlatformConfig{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			callsFile := filepath.Join(directory, "calls")
			t.Setenv("TEARDOWN_CALLS", callsFile)
			binary := filepath.Join(directory, "hypershift")
			if err := os.WriteFile(binary, []byte(`#!/bin/sh
if [ ! -f "$TEARDOWN_CALLS" ]; then
  printf '%s\n' destroy > "$TEARDOWN_CALLS"
  exit 1
fi
printf '%s\n' destroy >> "$TEARDOWN_CALLS"
`), 0700); err != nil {
				t.Fatal(err)
			}
			var calls []string
			var artifactDirectory string
			collectors := teardownDiagnosticCollectors{
				console: func(_ context.Context, _ lifecycle.ClusterEntry, artifactDir string) error {
					calls = append(calls, "console")
					return os.WriteFile(filepath.Join(artifactDir, "console.log"), []byte("initial console"), 0600)
				},
				dump: func(_ context.Context, options *clusterdump.DumpOptions) error {
					artifactDirectory = options.ArtifactDir
					if options.IsDumpingGuestCluster {
						calls = append(calls, "guest")
					}
					calls = append(calls, "management")
					return nil
				},
				journals: func(_ context.Context, _ lifecycle.ClusterEntry, artifactDir string) error {
					calls = append(calls, "bastion-create", "journals", "bastion-destroy")
					return os.WriteFile(filepath.Join(artifactDir, "journals.log"), []byte("initial journals"), 0600)
				},
			}
			entry := lifecycle.ClusterEntry{Name: "public", Namespace: "clusters", Variant: "public"}
			result := destroyClusterWithDiagnostics(t.Context(), binary, directory, entry, testCase.platform, collectors)
			if result.destroyError != nil || result.diagnosticError != nil {
				t.Fatalf("destroy=%v diagnostics=%v", result.destroyError, result.diagnosticError)
			}
			wantCalls := []string{"guest", "management", "management"}
			if testCase.aws {
				wantCalls = []string{"console", "guest", "management", "bastion-create", "journals", "bastion-destroy", "management"}
				for filename, expected := range map[string]string{"console.log": "initial console", "journals.log": "initial journals"} {
					content, err := os.ReadFile(filepath.Join(artifactDirectory, filename))
					if err != nil || string(content) != expected {
						t.Fatalf("initial artifact %s changed: content=%q err=%v", filename, content, err)
					}
				}
			}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Fatalf("collector calls=%v, want %v", calls, wantCalls)
			}
			attempts, err := os.ReadFile(callsFile)
			if err != nil || string(attempts) != "destroy\ndestroy\n" {
				t.Fatalf("destroy attempts=%q err=%v", attempts, err)
			}
		})
	}
}

func TestDestroyClusterWithDiagnosticCollectors(t *testing.T) {
	for _, testCase := range []struct {
		name                                                                       string
		dumpFailure, destroyFailure, artifacts, invalidArtifactDir, archiveFailure bool
		transientDestroyFailure                                                    bool
		retryDumpFailure, missingInputs                                            bool
	}{
		{name: "When diagnostics and destroy succeed, it should dump first and archive after destroy", artifacts: true},
		{name: "When the initial dump fails, it should still destroy and retain the original diagnostic error", artifacts: true, dumpFailure: true},
		{name: "When destroy fails, it should preserve partial diagnostics in an archive", artifacts: true, destroyFailure: true},
		{name: "When both operations fail, it should retain their independent errors", artifacts: true, dumpFailure: true, destroyFailure: true},
		{name: "When no artifact directory is configured, it should preserve destroy-only operation"},
		{name: "When artifact creation fails, it should still destroy and report the artifact error", artifacts: true, invalidArtifactDir: true},
		{name: "When archiving fails, it should retain the diagnostic error after successful destroy", artifacts: true, archiveFailure: true},
		{name: "When destroy initially fails, it should dump management diagnostics and retry before archiving", artifacts: true, transientDestroyFailure: true},
		{name: "When retry dumping fails, it should still retry cleanup without making the diagnostic fatal", artifacts: true, transientDestroyFailure: true, retryDumpFailure: true},
		{name: "When cleanup recovers, it should retain the original initial dump failure", artifacts: true, transientDestroyFailure: true, dumpFailure: true},
		{name: "When required inputs are missing, it should not retry or collect a retry dump", artifacts: true, destroyFailure: true, missingInputs: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			calls := filepath.Join(directory, "calls")
			t.Setenv("TEARDOWN_CALLS", calls)
			t.Setenv("DUMP_FAILURE", fmt.Sprint(testCase.dumpFailure))
			t.Setenv("DESTROY_FAILURE", fmt.Sprint(testCase.destroyFailure))
			t.Setenv("TRANSIENT_DESTROY_FAILURE", fmt.Sprint(testCase.transientDestroyFailure))
			t.Setenv("RETRY_DUMP_FAILURE", fmt.Sprint(testCase.retryDumpFailure))
			t.Setenv("TERMINAL_ERROR", "NoCredentialProviders")
			if testCase.missingInputs {
				t.Setenv("TERMINAL_ERROR", "required inputs are missing")
			}
			binary := filepath.Join(directory, "hypershift")
			script := `#!/bin/sh
printf '%s\n' "$1" >> "$TEARDOWN_CALLS"
if [ "$1" = dump ]; then
  for argument in "$@"; do
    case "$argument" in
      --artifact-dir=*) artifact=${argument#--artifact-dir=} ;;
      --dump-guest-cluster=*) guest_enabled=${argument#--dump-guest-cluster=}; printf '%s\n' "$argument" >> "$TEARDOWN_CALLS.modes" ;;
    esac
  done
  printf '%s\n' "$artifact" > "$TEARDOWN_CALLS.directory"
  printf '%s\n' 'partial diagnostic content' > "$artifact/partial.txt"
  if [ "$DUMP_FAILURE" = true ]; then exit 7; fi
  if [ "$RETRY_DUMP_FAILURE" = true ] && [ "$guest_enabled" = false ]; then exit 6; fi
else
  if [ -f "$TEARDOWN_CALLS.directory" ]; then
    read -r artifact < "$TEARDOWN_CALLS.directory"
    if [ -f "$artifact/hypershift-dump.tar.gz" ]; then exit 9; fi
    printf '%s\n' 'destroy attempted' > "$artifact/destroy-attempted.txt"
  fi
  if [ "$DESTROY_FAILURE" = true ]; then printf '%s\n' "$TERMINAL_ERROR" >&2; exit 8; fi
  if [ "$TRANSIENT_DESTROY_FAILURE" = true ] && [ ! -f "$TEARDOWN_CALLS.failed" ]; then
    printf '%s\n' 'failed' > "$TEARDOWN_CALLS.failed"
    exit 8
  fi
fi
`
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			artifactDir := ""
			if testCase.artifacts {
				artifactDir = filepath.Join(directory, "artifacts")
			}
			if testCase.invalidArtifactDir {
				if err := os.WriteFile(artifactDir, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.archiveFailure {
				t.Setenv("PATH", t.TempDir())
			}
			result := destroyClusterWithDiagnosticCollectors(t.Context(), binary, artifactDir, lifecycle.ClusterEntry{Name: "cluster", Namespace: "clusters", Variant: "public", InfraID: "infra"}, &lifecycle.AWSPlatformConfig{}, func(ctx context.Context, directory string, guest bool) error {
				command := exec.CommandContext(ctx, binary, "dump", "cluster", "--artifact-dir="+directory, fmt.Sprintf("--dump-guest-cluster=%t", guest), "--archive-dump=false")
				return command.Run()
			})
			if (result.destroyError != nil) != testCase.destroyFailure || (result.diagnosticError != nil) != (testCase.dumpFailure || testCase.invalidArtifactDir || testCase.archiveFailure) {
				t.Fatalf("destroy=%v diagnostic=%v", result.destroyError, result.diagnosticError)
			}
			if testCase.dumpFailure && !strings.Contains(result.diagnosticError.Error(), "exit status 7") {
				t.Fatalf("original dump error lost: %v", result.diagnosticError)
			}
			data, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			want := "destroy\n"
			if testCase.artifacts && !testCase.invalidArtifactDir {
				want = "dump\ndestroy\n"
			}
			if testCase.transientDestroyFailure {
				want = "dump\ndestroy\ndump\ndestroy\n"
				modes, err := os.ReadFile(calls + ".modes")
				if err != nil || string(modes) != "--dump-guest-cluster=true\n--dump-guest-cluster=false\n" {
					t.Fatalf("dump modes=%q error=%v", modes, err)
				}
			}
			if string(data) != want {
				t.Fatalf("operation order %q, want %q", data, want)
			}
			if !testCase.artifacts || testCase.invalidArtifactDir || testCase.archiveFailure {
				return
			}
			assertTeardownArchive(t, calls)
		})
	}
}

func TestCollectTeardownDiagnostics(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		aws, dumpFailure bool
	}{
		{name: "When AWS diagnostics run, it should collect console dump and journals in order despite nonfatal errors", aws: true},
		{name: "When the dump fails, it should preserve that error and still collect journals", aws: true, dumpFailure: true},
		{name: "When the platform is not AWS, it should run only the existing hosted cluster dump"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			original := errors.New("hosted cluster dump failed")
			var calls []string
			err := collectTeardownDiagnostics(t.Context(), testCase.aws,
				func(context.Context) error {
					calls = append(calls, "console")
					return errors.New("nonfatal console failure")
				},
				func(context.Context) error {
					calls = append(calls, "dump")
					if testCase.dumpFailure {
						return original
					}
					return nil
				},
				func(context.Context) error {
					calls = append(calls, "journals")
					return errors.New("nonfatal journal failure")
				},
			)
			wantCalls := []string{"dump"}
			if testCase.aws {
				wantCalls = []string{"console", "dump", "journals"}
			}
			if !reflect.DeepEqual(calls, wantCalls) || (err != nil) != testCase.dumpFailure || testCase.dumpFailure && !errors.Is(err, original) {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
		})
	}
}

func TestDumpHostedClusterDiagnostics(t *testing.T) {
	for _, testCase := range []struct {
		name                               string
		guest, loop, dumpFailure, canceled bool
	}{
		{name: "When initial diagnostics run, it should dump the hosted cluster with a bounded native checker", guest: true},
		{name: "When retry diagnostics run, it should disable hosted cluster dumping"},
		{name: "When a current or previous pod log contains an update loop, it should fail initial diagnostics", guest: true, loop: true},
		{name: "When dump and checker fail, it should retain both errors", loop: true, dumpFailure: true},
		{name: "When the parent is canceled, it should preserve cancellation", canceled: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.canceled {
				cancel()
			}
			original := errors.New("native dump failed")
			directory := t.TempDir()
			err := dumpHostedClusterDiagnostics(ctx, directory, lifecycle.ClusterEntry{Name: "hosted", Namespace: "clusters", Variant: "public"}, &lifecycle.AWSPlatformConfig{}, testCase.guest, func(ctx context.Context, options *clusterdump.DumpOptions) error {
				deadline, bounded := ctx.Deadline()
				if !bounded || time.Until(deadline) > teardownDiagnosticTimeout || options.Name != "hosted" || options.Namespace != "clusters" || options.ArtifactDir != directory || options.IsDumpingGuestCluster != testCase.guest || options.ArchiveDump || len(options.LogCheckers) != 1 || options.ClientProvider == nil {
					t.Fatalf("invalid native diagnostic options: %#v", options)
				}
				options.Log.Info("dump progress")
				options.LogCheckers[0]("clean.log", []byte("no loop"))
				if testCase.loop {
					options.LogCheckers[0]("current.log", []byte(upsert.LoopDetectorWarningMessage))
					options.LogCheckers[0]("previous.log", []byte(upsert.LoopDetectorWarningMessage))
				}
				if testCase.dumpFailure {
					return original
				}
				return ctx.Err()
			})
			if (err != nil) != (testCase.loop || testCase.dumpFailure || testCase.canceled) {
				t.Fatalf("error=%v", err)
			}
			if testCase.loop && (!strings.Contains(err.Error(), "current.log") || !strings.Contains(err.Error(), "previous.log")) {
				t.Fatalf("log checker errors lost: %v", err)
			}
			if testCase.dumpFailure && !errors.Is(err, original) {
				t.Fatalf("dump error lost: %v", err)
			}
			if testCase.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			data, readError := os.ReadFile(filepath.Join(directory, "dump.log"))
			if readError != nil || !strings.Contains(string(data), "dump progress") {
				t.Fatalf("dump log=%q error=%v", data, readError)
			}
		})
	}
	t.Run("When artifacts cannot be opened, it should fail before invoking the dump", func(t *testing.T) {
		err := dumpHostedClusterDiagnostics(t.Context(), filepath.Join(t.TempDir(), "missing"), lifecycle.ClusterEntry{}, &lifecycle.AWSPlatformConfig{}, false, func(context.Context, *clusterdump.DumpOptions) error {
			t.Fatal("dump invoked without artifacts")
			return nil
		})
		if err == nil {
			t.Fatal("artifact error ignored")
		}
	})
}

func assertTeardownArchive(t *testing.T, calls string) {
	t.Helper()
	data, err := os.ReadFile(calls + ".directory")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(strings.TrimSpace(string(data)), "hypershift-dump.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	contents := map[string]string{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		contents[header.Name] = string(body)
	}
	if !reflect.DeepEqual(contents, map[string]string{"partial.txt": "partial diagnostic content\n", "destroy-attempted.txt": "destroy attempted\n"}) {
		t.Fatalf("partial/post-destroy archive content=%v", contents)
	}
}

func TestRetryDestroy(t *testing.T) {
	t.Run("When consecutive errors repeat or change, it should log only each changed error", func(t *testing.T) {
		var messages bytes.Buffer
		previousOutput := log.Writer()
		log.SetOutput(&messages)
		defer log.SetOutput(previousOutput)
		attempts, dumps := 0, 0
		err := retryDestroy(t.Context(), 0, func(context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.New("first")
			}
			if attempts == 3 {
				return errors.New("second")
			}
			return nil
		}, func(context.Context) { dumps++ })
		if err != nil || attempts != 4 || dumps != 3 || strings.Count(messages.String(), "will retry") != 2 {
			t.Fatalf("error=%v attempts=%d dumps=%d logs=%s", err, attempts, dumps, &messages)
		}
	})
	t.Run("When cancellation follows a failed destroy, it should retain both causes", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		original := errors.New("destroy failed")
		attempts, dumps := 0, 0
		err := retryDestroy(ctx, time.Hour, func(context.Context) error {
			attempts++
			return original
		}, func(context.Context) {
			dumps++
			cancel()
		})
		if !errors.Is(err, original) || !errors.Is(err, context.Canceled) || attempts != 1 || dumps != 1 {
			t.Fatalf("error=%v attempts=%d dumps=%d", err, attempts, dumps)
		}
	})
	t.Run("When the command cannot run, it should not retry or dump", func(t *testing.T) {
		original := errors.New("missing executable")
		attempts := 0
		err := retryDestroy(t.Context(), time.Millisecond, func(context.Context) error {
			attempts++
			return &destroyCommandError{err: original}
		}, func(context.Context) { t.Fatal("dump after terminal failure") })
		if !errors.Is(err, original) || attempts != 1 {
			t.Fatalf("error=%v attempts=%d", err, attempts)
		}
	})
}

func TestDestroyErrorOutput(t *testing.T) {
	for _, marker := range []string{"required inputs are missing", "NoCredentialProviders"} {
		t.Run("When "+marker+" spans writes, it should retain terminal identity with bounded memory", func(t *testing.T) {
			output := &destroyErrorOutput{digest: sha256.New()}
			prefix := strings.Repeat("x", 1024)
			for _, piece := range []string{prefix, marker[:5], marker[5:], prefix} {
				if _, err := output.Write([]byte(piece)); err != nil {
					t.Fatal(err)
				}
			}
			want := sha256.Sum256([]byte(prefix + marker + prefix))
			if !output.terminal || len(output.tail) > 64 || !bytes.Equal(output.digest.Sum(nil), want[:]) {
				t.Fatalf("terminal=%t retained=%d", output.terminal, len(output.tail))
			}
		})
	}
}

func TestDestroyTimeout(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		value   string
		want    time.Duration
		invalid bool
	}{
		{name: "When absent, it should use a finite total budget distinct from per-attempt grace", want: 2 * time.Hour},
		{name: "When configured, it should preserve the explicit duration", value: "90m", want: 90 * time.Minute},
		{name: "When malformed, it should reject the budget", value: "invalid", invalid: true},
		{name: "When zero, it should reject an unbounded budget", value: "0s", invalid: true},
		{name: "When negative, it should reject the budget", value: "-1m", invalid: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := destroyTimeout(testCase.value)
			if (err != nil) != testCase.invalid || got != testCase.want {
				t.Fatalf("duration=%s error=%v", got, err)
			}
		})
	}
}

func TestTeardownArtifactDirectory(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../cluster", "cluster/child"} {
		t.Run("When cluster name is "+fmt.Sprintf("%q", name)+", it should reject unsafe artifact paths", func(t *testing.T) {
			if _, err := teardownArtifactDirectory(t.TempDir(), name); err == nil {
				t.Fatal("invalid artifact path accepted")
			}
		})
	}
	t.Run("When artifacts are disabled, it should not create a directory", func(t *testing.T) {
		if path, err := teardownArtifactDirectory("", "cluster"); err != nil || path != "" {
			t.Fatalf("path=%s error=%v", path, err)
		}
	})
	t.Run("When cleanup is retried, it should keep attempts in separate directories", func(t *testing.T) {
		parent := t.TempDir()
		first, err := teardownArtifactDirectory(parent, "cluster")
		if err != nil {
			t.Fatal(err)
		}
		second, err := teardownArtifactDirectory(parent, "cluster")
		if err != nil || first == second {
			t.Fatalf("directory collision: %s %s %v", first, second, err)
		}
	})
}
