package azure

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
)

const (
	journalTimeout   = 2 * time.Minute
	journalLineLimit = 100000
	journalByteLimit = 32 << 20
)

var journalKinds = []string{"journal", "kubelet", "crio"}
var errNoSSHAddress = errors.New("worker SSH address is unavailable")

// JournalCollector writes stdout only; command failure must be returned separately.
type JournalCollector func(context.Context, capiazure.AzureMachine, string, io.Writer) error

// SSHOptions enables fallback through already configured worker SSH connectivity.
// No network rules, agents or bastions are created by the collector.
type SSHOptions struct {
	PrivateKeyFile string
	JumpHost       string
	KnownHostsFile string
}

// SSHOptionsFromEnv returns nil unless AZURE_JOURNAL_SSH_KEY explicitly enables SSH.
func SSHOptionsFromEnv() *SSHOptions {
	key := os.Getenv("AZURE_JOURNAL_SSH_KEY")
	if key == "" {
		return nil
	}
	return &SSHOptions{PrivateKeyFile: key, JumpHost: os.Getenv("AZURE_JOURNAL_SSH_JUMP_HOST"), KnownHostsFile: os.Getenv("AZURE_JOURNAL_SSH_KNOWN_HOSTS")}
}

func collectJournals(ctx context.Context, machine capiazure.AzureMachine, dir string, api JournalCollector, apiErr error, ssh *SSHOptions) map[string]ArtifactResult {
	results := make(map[string]ArtifactResult, len(journalKinds))
	for _, kind := range journalKinds {
		path := filepath.Join(journalsDirectory, machine.Name+"."+kind+".log.gz")
		result := ArtifactResult{Status: StatusSkipped, Reason: "hosted API journal access is unavailable"}
		if api != nil {
			result = collectJournal(ctx, dir, path, func(ctx context.Context, out io.Writer) error { return api(ctx, machine, kind, out) })
		} else if apiErr != nil {
			result = failedResult(apiErr, "failed to initialize hosted API journal access")
		}
		attempts := []TransportAttempt{{Transport: "api", Status: result.Status, Reason: result.Reason}}
		if result.Status != StatusCollected && ssh != nil && ssh.PrivateKeyFile != "" {
			result = collectJournal(ctx, dir, path, func(ctx context.Context, out io.Writer) error { return collectSSHJournal(ctx, machine, kind, ssh, out) })
			attempts = append(attempts, TransportAttempt{Transport: "ssh", Status: result.Status, Reason: result.Reason})
		} else if result.Status != StatusCollected {
			_ = os.Remove(filepath.Join(dir, path))
			attempts = append(attempts, TransportAttempt{Transport: "ssh", Status: StatusSkipped, Reason: "SSH access is not configured"})
		}
		result.Attempts = attempts
		results[kind] = result
	}
	return results
}

func collectJournal(ctx context.Context, dir, path string, collect func(context.Context, io.Writer) error) ArtifactResult {
	result := ArtifactResult{Compressed: true, LineLimit: journalLineLimit, ByteLimit: journalByteLimit}
	dest := filepath.Join(dir, path)
	_ = os.Remove(dest)
	if ctx.Err() != nil {
		return failedResult(ctx.Err(), "journal collection canceled")
	}
	file, err := os.CreateTemp(filepath.Dir(dest), ".journal-*")
	if err != nil {
		return failedResult(err, "failed to create journal artifact")
	}
	defer os.Remove(file.Name())
	compressed := gzip.NewWriter(file)
	writer := &boundedJournalWriter{out: compressed}
	requestCtx, cancel := context.WithTimeout(ctx, journalTimeout)
	defer cancel()
	err = collect(requestCtx, writer)
	closeErr := compressed.Close()
	fileErr := file.Close()
	result.Bytes = writer.bytes
	result.Truncated = writer.truncated || writer.lines >= journalLineLimit
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = fileErr
	}
	if err != nil {
		failure := failedResult(err, "journal command failed")
		if errors.Is(err, errNoHostedNode) || errors.Is(err, errNoSSHAddress) {
			failure.Status, failure.Reason = StatusSkipped, err.Error()
		}
		result.Status, result.Reason = failure.Status, failure.Reason
		return result
	}
	if !usefulJournalContent(writer.prefix.String()) {
		result.Status, result.Reason = StatusEmpty, "journal output is empty"
		return result
	}
	if err := os.Rename(file.Name(), dest); err != nil {
		return failedResult(err, "failed to save journal artifact")
	}
	result.Status, result.Path = StatusCollected, path
	return result
}

type boundedJournalWriter struct {
	out       io.Writer
	bytes     int64
	lines     int
	prefix    bytes.Buffer
	truncated bool
}

func (w *boundedJournalWriter) Write(content []byte) (int, error) {
	remaining := int(journalByteLimit - w.bytes)
	accepted := content
	if len(content) > remaining {
		w.truncated = true
		accepted = content[:remaining]
	}
	if w.prefix.Len() < 4096 {
		n := min(len(accepted), 4096-w.prefix.Len())
		_, _ = w.prefix.Write(accepted[:n])
	}
	n, err := w.out.Write(accepted)
	w.bytes += int64(n)
	w.lines += bytes.Count(accepted[:n], []byte{'\n'})
	if err != nil {
		return n, err
	}
	// Drain stdout after the bound so the command can finish and its exit status
	// can still be validated. Only the bounded content is written to disk.
	return len(content), nil
}

func collectSSHJournal(ctx context.Context, machine capiazure.AzureMachine, kind string, opts *SSHOptions, out io.Writer) error {
	address := sshAddress(machine, opts.JumpHost != "")
	if address == "" {
		return errNoSSHAddress
	}
	args := []string{"-n", "-T", "-i", opts.PrivateKeyFile, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=2"}
	if opts.KnownHostsFile != "" {
		args = append(args, "-o", "UserKnownHostsFile="+opts.KnownHostsFile)
	}
	if opts.JumpHost != "" {
		args = append(args, "-J", opts.JumpHost)
	}
	command := "sudo -n journalctl --no-pager --boot=0 --lines=100000"
	if kind != "journal" {
		command += " --unit=" + kind
	}
	args = append(args, "core@"+address, command)
	return runJournalCommand(ctx, "ssh", args, out)
}

func sshAddress(machine capiazure.AzureMachine, viaJumpHost bool) string {
	preferred := []corev1.NodeAddressType{corev1.NodeExternalIP, corev1.NodeInternalIP}
	if viaJumpHost {
		preferred[0], preferred[1] = preferred[1], preferred[0]
	}
	for _, kind := range preferred {
		for _, address := range machine.Status.Addresses {
			if address.Type == kind && net.ParseIP(address.Address) != nil {
				return address.Address
			}
		}
	}
	return ""
}

func runJournalCommand(ctx context.Context, binary string, args []string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = time.Second
	cmd.Stdout = out
	// stderr is never mixed with journal contents or command arguments logged.
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s journal command exited unsuccessfully", filepath.Base(binary))
	}
	return nil
}

func normalizedProviderID(providerID string) string {
	sub, rg, vm, err := parseAzureVMResourceID(providerID)
	if err != nil {
		return ""
	}
	return strings.ToLower(sub + "/" + rg + "/" + vm)
}
