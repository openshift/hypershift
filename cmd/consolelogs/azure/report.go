package azure

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	StatusCollected = "collected"
	StatusSkipped   = "skipped"
	StatusEmpty     = "empty"
	StatusFailed    = "failed"
	StatusTimedOut  = "timed-out"
)

// CollectionReport records outcomes, including machines without guest access.
type CollectionReport struct {
	Machines []MachineResult `json:"machines"`
	Error    string          `json:"error,omitempty"`
}

// MachineResult associates artifacts with their AzureMachine rather than a Node.
type MachineResult struct {
	Name            string                    `json:"name"`
	BootDiagnostics string                    `json:"bootDiagnostics"`
	Serial          ArtifactResult            `json:"serial"`
	Journals        map[string]ArtifactResult `json:"journals"`
}

// ArtifactResult reports only completed artifacts as collected. Bytes counts
// uncompressed content. Truncated records a reached line or byte bound.
type ArtifactResult struct {
	Status     string             `json:"status"`
	Reason     string             `json:"reason,omitempty"`
	Path       string             `json:"path,omitempty"`
	Bytes      int64              `json:"bytes,omitempty"`
	Compressed bool               `json:"compressed,omitempty"`
	LineLimit  int                `json:"lineLimit,omitempty"`
	ByteLimit  int64              `json:"byteLimit,omitempty"`
	Truncated  bool               `json:"truncated,omitempty"`
	Attempts   []TransportAttempt `json:"attempts,omitempty"`
}

// TransportAttempt preserves the API failure when an SSH fallback succeeds.
type TransportAttempt struct {
	Transport string `json:"transport"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

func failedResult(err error, reason string) ArtifactResult {
	status := StatusFailed
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		status = StatusTimedOut
	}
	return ArtifactResult{Status: status, Reason: reason}
}

func writeReport(artifactDir string, report CollectionReport) error {
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return fmt.Errorf("failed to create diagnostics directory: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode diagnostics summary: %w", err)
	}
	return os.WriteFile(filepath.Join(artifactDir, "machine-diagnostics.json"), append(data, '\n'), 0644)
}

// VerifyArtifacts requires fresh collected artifacts for every expected machine.
// Callers must supply their independent pre-collection machine inventory. Empty
// inventories, duplicate entries, missing files and empty gzip payloads fail.
// Journals are required only when requireJournals is true.
func VerifyArtifacts(artifactDir string, report CollectionReport, expected []string, requireJournals bool) error {
	if len(expected) == 0 {
		return errors.New("expected at least one Azure worker")
	}
	if report.Error != "" {
		return errors.New(report.Error)
	}
	machines := make(map[string]MachineResult, len(report.Machines))
	for _, machine := range report.Machines {
		if _, exists := machines[machine.Name]; exists {
			return fmt.Errorf("duplicate machine %s in diagnostics summary", machine.Name)
		}
		machines[machine.Name] = machine
	}
	seen := make(map[string]bool, len(expected))
	for _, name := range expected {
		if seen[name] {
			return fmt.Errorf("duplicate expected machine %s", name)
		}
		seen[name] = true
		machine, exists := machines[name]
		if !exists {
			return fmt.Errorf("AzureMachine %s is missing from diagnostics summary", name)
		}
		if err := verifyArtifact(artifactDir, machine.Serial); err != nil {
			return fmt.Errorf("AzureMachine %s serial: %w", name, err)
		}
		if requireJournals {
			for _, kind := range journalKinds {
				if err := verifyArtifact(artifactDir, machine.Journals[kind]); err != nil {
					return fmt.Errorf("AzureMachine %s %s: %w", name, kind, err)
				}
			}
		}
	}
	return nil
}

func verifyArtifact(artifactDir string, result ArtifactResult) error {
	if result.Status != StatusCollected || result.Bytes <= 0 {
		return fmt.Errorf("artifact was not collected (%s): %s", result.Status, result.Reason)
	}
	if !filepath.IsLocal(result.Path) {
		return errors.New("artifact path is missing or invalid")
	}
	file, err := os.Open(filepath.Join(artifactDir, result.Path))
	if err != nil {
		return err
	}
	defer file.Close()
	var reader io.Reader = file
	if result.Compressed {
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer compressed.Close()
		reader = compressed
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxSerialLogSize+1))
	if err != nil {
		return err
	}
	if len(content) > maxSerialLogSize || int64(len(content)) != result.Bytes || !usefulJournalContent(string(content)) {
		return errors.New("artifact content is empty, incomplete, or differs from the collection summary")
	}
	return nil
}

func usefulJournalContent(content string) bool {
	content = strings.TrimSpace(content)
	return content != "" && content != "-- No entries --" && content != "No journal files were found."
}
