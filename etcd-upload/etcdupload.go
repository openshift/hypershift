package etcdupload

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

type options struct {
	snapshotPath    string
	snapshotDir     string
	storageType     string
	bucket          string
	region          string
	keyPrefix       string
	credentialsFile string
	kmsKeyARN       string

	// Azure-specific
	container       string
	storageAccount  string
	encryptionScope string
	authType        string
}

func NewStartCommand() *cobra.Command {
	opts := options{}

	cmd := &cobra.Command{
		Use:          "etcd-upload",
		Short:        "Upload an etcd snapshot to cloud storage",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			return run(ctx, opts)
		},
	}

	// Common flags
	cmd.Flags().StringVar(&opts.snapshotPath, "snapshot-path", "", "path to a single etcd snapshot file to upload (mutually exclusive with --snapshot-dir)")
	cmd.Flags().StringVar(&opts.snapshotDir, "snapshot-dir", "", "directory containing per-shard snapshot .db files to upload (mutually exclusive with --snapshot-path)")
	cmd.Flags().StringVar(&opts.storageType, "storage-type", "", "cloud storage backend type (S3 or AzureBlob)")
	cmd.Flags().StringVar(&opts.keyPrefix, "key-prefix", "", "key prefix for the backup file in cloud storage")
	cmd.Flags().StringVar(&opts.credentialsFile, "credentials-file", "", "path to cloud credentials file")

	// AWS-specific flags
	cmd.Flags().StringVar(&opts.bucket, "aws-bucket", "", "[AWS] S3 bucket name")
	cmd.Flags().StringVar(&opts.region, "aws-region", "", "[AWS] region of the S3 bucket")
	cmd.Flags().StringVar(&opts.kmsKeyARN, "aws-kms-key-arn", "", "[AWS] ARN of the KMS key for SSE-KMS encryption (optional)")

	// Azure-specific flags
	cmd.Flags().StringVar(&opts.container, "azure-container", "", "[Azure] Blob Storage container name")
	cmd.Flags().StringVar(&opts.storageAccount, "azure-storage-account", "", "[Azure] Storage Account name")
	cmd.Flags().StringVar(&opts.encryptionScope, "azure-encryption-scope", "", "[Azure] encryption scope for server-side encryption (optional)")
	cmd.Flags().StringVar(&opts.authType, "azure-auth-type", "client-secret", "[Azure] authentication type: client-secret (default) or managed-identity (ARO HCP)")

	_ = cmd.MarkFlagRequired("storage-type")
	_ = cmd.MarkFlagRequired("key-prefix")
	cmd.MarkFlagsMutuallyExclusive("snapshot-path", "snapshot-dir")
	cmd.MarkFlagsOneRequired("snapshot-path", "snapshot-dir")

	return cmd
}

// shardSnapshot is the JSON format written to the termination log when
// uploading multiple shard snapshots.
type shardSnapshot struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func run(ctx context.Context, opts options) error {
	// Defense-in-depth: Cobra enforces these via MarkFlagsOneRequired and
	// MarkFlagsMutuallyExclusive, but run() can be called directly from tests.
	if opts.snapshotPath == "" && opts.snapshotDir == "" {
		return fmt.Errorf("either --snapshot-path or --snapshot-dir must be specified")
	}
	if opts.snapshotPath != "" && opts.snapshotDir != "" {
		return fmt.Errorf("--snapshot-path and --snapshot-dir are mutually exclusive")
	}

	opts.keyPrefix = strings.TrimSuffix(opts.keyPrefix, "/")

	uploader, err := newUploader(ctx, opts)
	if err != nil {
		return err
	}

	if opts.snapshotDir != "" {
		return runDir(ctx, opts, uploader)
	}
	return runSingle(ctx, opts, uploader)
}

// runSingle uploads a single snapshot file (backward-compat with --snapshot-path).
func runSingle(ctx context.Context, opts options, uploader Uploader) error {
	if _, err := os.Stat(opts.snapshotPath); err != nil {
		return fmt.Errorf("snapshot file not accessible: %w", err)
	}

	key := fmt.Sprintf("%s/%d%s", opts.keyPrefix, time.Now().Unix(), filepath.Ext(opts.snapshotPath))

	result, err := uploader.Upload(ctx, opts.snapshotPath, key)
	if err != nil {
		return fmt.Errorf("failed to upload snapshot: %w", err)
	}

	fmt.Println(result.URL)

	// Write the URL to the termination log so the controller can read it.
	if err := os.WriteFile("/dev/termination-log", []byte(result.URL), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write termination log: %v\n", err)
	}

	return nil
}

// runDir uploads all .db files in the given directory as per-shard snapshots.
// Each file is uploaded with key <prefix>/<timestamp>/<shard-name>.db.
// Files are processed in lexicographic order (os.ReadDir guarantees sorted results)
// so upload order is deterministic.
// The termination log is written as a JSON array of {name, url} objects.
func runDir(ctx context.Context, opts options, uploader Uploader) error {
	entries, err := os.ReadDir(opts.snapshotDir)
	if err != nil {
		return fmt.Errorf("failed to read snapshot directory: %w", err)
	}

	timestamp := time.Now().Unix()
	var snapshots []shardSnapshot

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".db" {
			continue
		}

		snapshotPath := filepath.Join(opts.snapshotDir, entry.Name())
		shardName := strings.TrimSuffix(entry.Name(), ".db")
		key := fmt.Sprintf("%s/%d/%s.db", opts.keyPrefix, timestamp, shardName)

		result, err := uploader.Upload(ctx, snapshotPath, key)
		if err != nil {
			return fmt.Errorf("failed to upload shard %q snapshot: %w", shardName, err)
		}

		fmt.Printf("uploaded %s: %s\n", shardName, result.URL)
		snapshots = append(snapshots, shardSnapshot{
			Name: shardName,
			URL:  result.URL,
		})
	}

	if len(snapshots) == 0 {
		return fmt.Errorf("no .db files found in snapshot directory %q", opts.snapshotDir)
	}

	// Write JSON array to termination log.
	jsonBytes, err := json.Marshal(snapshots)
	if err != nil {
		return fmt.Errorf("failed to marshal shard snapshots: %w", err)
	}

	if err := os.WriteFile("/dev/termination-log", jsonBytes, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write termination log: %v\n", err)
	}

	return nil
}

func newUploader(ctx context.Context, opts options) (Uploader, error) {
	switch opts.storageType {
	case "S3":
		return NewS3Uploader(ctx, opts.bucket, opts.region, opts.credentialsFile, opts.kmsKeyARN)
	case "AzureBlob":
		return NewAzureBlobUploader(ctx, opts.container, opts.storageAccount, opts.credentialsFile, opts.encryptionScope, opts.authType)
	default:
		return nil, fmt.Errorf("unsupported storage type: %q (must be S3 or AzureBlob)", opts.storageType)
	}
}
