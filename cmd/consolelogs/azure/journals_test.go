package azure

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
)

func TestCollectJournal(t *testing.T) {
	t.Run("When a successful journal exceeds the byte bound, it should save a valid truncated artifact", func(t *testing.T) {
		dir := t.TempDir()
		result := collectJournal(t.Context(), dir, "worker.log.gz", func(_ context.Context, out io.Writer) error {
			_, err := io.WriteString(out, strings.Repeat("x", journalByteLimit+1))
			return err
		})
		if result.Status != StatusCollected || !result.Truncated || result.Bytes != journalByteLimit {
			t.Fatalf("unexpected bounded artifact: %+v", result)
		}
		if err := verifyArtifact(dir, result); err != nil {
			t.Fatal(err)
		}
	})
	for _, tt := range []struct {
		name, output string
		err          error
		want         string
	}{
		{"When journal output is useful, it should save valid compressed content", "journal output\n", nil, StatusCollected},
		{"When stdout is empty, it should report empty output", "", nil, StatusEmpty},
		{"When no entries are returned, it should report empty output", "-- No entries --\n", nil, StatusEmpty},
		{"When a command fails after writing, it should discard partial output", "partial output", errors.New("failed"), StatusFailed},
		{"When the transport times out, it should report a timeout", "", context.DeadlineExceeded, StatusTimedOut},
		{"When a VM has no matching Node, it should visibly skip API access", "", errNoHostedNode, StatusSkipped},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			result := collectJournal(t.Context(), dir, "worker.log.gz", func(_ context.Context, out io.Writer) error {
				_, err := io.WriteString(out, tt.output)
				if err != nil {
					return err
				}
				return tt.err
			})
			if result.Status != tt.want {
				t.Fatalf("got %+v, want %s", result, tt.want)
			}
			if tt.want == StatusCollected {
				if err := verifyArtifact(dir, result); err != nil {
					t.Fatal(err)
				}
			} else if _, err := os.Stat(filepath.Join(dir, "worker.log.gz")); !os.IsNotExist(err) {
				t.Fatal("failed collection left a file")
			}
		})
	}
}

func TestCollectJournals(t *testing.T) {
	for _, tt := range []struct {
		name, apiOutput string
		apiErr          error
		ssh             bool
		want, transport string
	}{
		{"When API collection succeeds, it should not invoke SSH", "API journal", nil, true, StatusCollected, "api"},
		{"When API collection fails and SSH is configured, it should use SSH", "", errors.New("API unavailable"), true, StatusCollected, "ssh"},
		{"When API returns empty output and SSH is configured, it should use SSH", "", nil, true, StatusCollected, "ssh"},
		{"When API fails and SSH is not configured, it should record the failed attempt", "", errors.New("API unavailable"), false, StatusFailed, "api"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			binDir := t.TempDir()
			marker := filepath.Join(binDir, "ssh-called")
			if err := os.WriteFile(filepath.Join(binDir, "ssh"), []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf 'SSH journal output\\n'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, journalsDirectory), 0755); err != nil {
				t.Fatal(err)
			}
			machine := azureMachineWithProviderID("worker-0", testProviderID)
			machine.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.4"}}
			api := func(_ context.Context, _ capiazure.AzureMachine, _ string, out io.Writer) error {
				_, _ = io.WriteString(out, tt.apiOutput)
				return tt.apiErr
			}
			var ssh *SSHOptions
			if tt.ssh {
				ssh = &SSHOptions{PrivateKeyFile: filepath.Join(binDir, "key")}
			}
			results := collectJournals(t.Context(), machine, dir, api, nil, ssh)
			for kind, result := range results {
				if result.Status != tt.want {
					t.Fatalf("%s: %+v", kind, result)
				}
				if tt.want == StatusCollected {
					last := result.Attempts[len(result.Attempts)-1]
					if last.Transport != tt.transport {
						t.Fatalf("wrong transport: %+v", result)
					}
				}
			}
			_, err := os.Stat(marker)
			if (err == nil) != (tt.transport == "ssh") {
				t.Fatalf("unexpected SSH invocation: %v", err)
			}
		})
	}
}

func TestRunJournalCommand(t *testing.T) {
	t.Run("When a command emits stderr and fails, it should keep stderr out of stdout", func(t *testing.T) {
		var out bytes.Buffer
		err := runJournalCommand(t.Context(), "sh", []string{"-c", "printf 'error text' >&2; exit 1"}, &out)
		if err == nil || out.Len() != 0 {
			t.Fatalf("got output %q and error %v", out.String(), err)
		}
	})
	t.Run("When a running command is canceled, it should stop within a bounded wait", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := runJournalCommand(ctx, "sh", []string{"-c", "sleep 10"}, io.Discard)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("cancellation did not stop the command")
		}
	})
}

func TestBoundedJournalWriter(t *testing.T) {
	t.Run("When stdout exceeds the byte limit, it should truncate storage and keep draining", func(t *testing.T) {
		var out bytes.Buffer
		w := &boundedJournalWriter{out: &out, bytes: journalByteLimit - 1}
		n, err := w.Write([]byte("xx"))
		if err != nil || n != 2 || !w.truncated || w.bytes != journalByteLimit || out.String() != "x" {
			t.Fatalf("byte limit was not enforced: n=%d, err=%v, writer=%+v", n, err, w)
		}
	})
}

func TestSSHAddress(t *testing.T) {
	t.Run("When a jump host is used, it should prefer the private worker address", func(t *testing.T) {
		machine := capiazure.AzureMachine{Status: capiazure.AzureMachineStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.4"}, {Type: corev1.NodeExternalIP, Address: "192.0.2.4"}}}}
		if sshAddress(machine, false) != "192.0.2.4" || sshAddress(machine, true) != "10.0.0.4" {
			t.Fatal("SSH address preference ignored network topology")
		}
	})
}

func TestNormalizedProviderID(t *testing.T) {
	t.Run("When Azure IDs differ in casing, it should normalize them to the same identity", func(t *testing.T) {
		if normalizedProviderID(strings.ToUpper(testProviderID)) != normalizedProviderID(testProviderID) {
			t.Fatal("provider ID comparison should ignore Azure ID casing")
		}
	})
}

func TestSSHOptionsFromEnv(t *testing.T) {
	t.Run("When a key is not configured, it should leave SSH disabled", func(t *testing.T) {
		t.Setenv("AZURE_JOURNAL_SSH_KEY", "")
		if SSHOptionsFromEnv() != nil {
			t.Fatal("SSH was implicitly enabled")
		}
	})
	t.Run("When explicit SSH access is configured, it should use its key and network settings", func(t *testing.T) {
		t.Setenv("AZURE_JOURNAL_SSH_KEY", "/etc/ssh/worker-key")
		t.Setenv("AZURE_JOURNAL_SSH_JUMP_HOST", "core@192.0.2.10")
		t.Setenv("AZURE_JOURNAL_SSH_KNOWN_HOSTS", "/etc/ssh/known_hosts")
		opts := SSHOptionsFromEnv()
		if opts.PrivateKeyFile != "/etc/ssh/worker-key" || opts.JumpHost != "core@192.0.2.10" || opts.KnownHostsFile != "/etc/ssh/known_hosts" {
			t.Fatalf("incorrect SSH access configuration: %+v", opts)
		}
	})
}
