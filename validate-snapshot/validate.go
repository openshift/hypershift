package validatesnapshot

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

type options struct {
	snapshotDir string
}

func NewStartCommand() *cobra.Command {
	opts := options{}

	cmd := &cobra.Command{
		Use:          "validate-snapshot",
		Short:        "Validate an extracted etcd snapshot archive before restore",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			return run(ctx, opts)
		},
	}

	cmd.Flags().StringVar(&opts.snapshotDir, "snapshot-dir", "/snapshot", "directory containing extracted snapshot")

	return cmd
}

func run(_ context.Context, opts options) error {
	if err := validateCompleteness(opts.snapshotDir); err != nil {
		writeTerminationMessage(fmt.Sprintf("validation-failed:completeness:%s", err))
		return err
	}

	secretsDir := filepath.Join(opts.snapshotDir, "secrets")
	if err := validateSecretFiles(secretsDir); err != nil {
		writeTerminationMessage(fmt.Sprintf("validation-failed:secret-format:%s", err))
		return err
	}

	if err := validateDigest(opts.snapshotDir); err != nil {
		writeTerminationMessage(fmt.Sprintf("validation-failed:digest-mismatch:%s", err))
		return err
	}

	fmt.Println("all validations passed")
	writeTerminationMessage("validation-passed")
	return nil
}

func validateCompleteness(snapshotDir string) error {
	snapshotPath := filepath.Join(snapshotDir, "snapshot.db")
	info, err := os.Stat(snapshotPath)
	if err != nil {
		return fmt.Errorf("snapshot.db not found: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("snapshot.db is empty")
	}

	secretsDir := filepath.Join(snapshotDir, "secrets")
	entries, err := os.ReadDir(secretsDir)
	if err != nil {
		return fmt.Errorf("secrets directory not found: %w", err)
	}

	hasJSON := false
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			hasJSON = true
			break
		}
	}
	if !hasJSON {
		return fmt.Errorf("no .json files found in secrets directory")
	}

	return nil
}

func validateSecretFiles(secretsDir string) error {
	entries, err := os.ReadDir(secretsDir)
	if err != nil {
		return fmt.Errorf("failed to read secrets directory: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(secretsDir, e.Name())
		if err := validateSecretFile(path); err != nil {
			return fmt.Errorf("invalid secret file %s: %w", e.Name(), err)
		}
	}

	return nil
}

func validateSecretFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(data, &secret); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	if secret.Data == nil {
		return fmt.Errorf("missing \"data\" field")
	}

	for key, val := range secret.Data {
		if _, err := base64.StdEncoding.DecodeString(val); err != nil {
			return fmt.Errorf("value for key %q is not valid base64: %w", key, err)
		}
	}

	return nil
}

func validateDigest(snapshotDir string) error {
	digestPath := filepath.Join(snapshotDir, ".expected-sha256")
	expectedBytes, err := os.ReadFile(digestPath)
	if os.IsNotExist(err) {
		fmt.Println("no .expected-sha256 found, skipping digest verification")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read expected digest: %w", err)
	}

	expected := strings.TrimSpace(string(expectedBytes))
	if expected == "" {
		fmt.Println("empty .expected-sha256, skipping digest verification")
		return nil
	}

	archivePath := filepath.Join(snapshotDir, ".archive.tar.gz")
	actual, err := computeSHA256(archivePath)
	if err != nil {
		return fmt.Errorf("failed to compute archive digest: %w", err)
	}

	if actual != expected {
		return fmt.Errorf("digest mismatch: expected %s, got %s", expected, actual)
	}

	fmt.Println("digest verification passed")
	return nil
}

func computeSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeTerminationMessage(msg string) {
	if err := os.WriteFile("/dev/termination-log", []byte(msg), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write termination log: %v\n", err)
	}
}
