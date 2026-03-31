package etcdupload

import (
	"context"
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
	cmd.Flags().StringVar(&opts.snapshotPath, "snapshot-path", "", "path to the etcd snapshot file to upload")
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

	_ = cmd.MarkFlagRequired("snapshot-path")
	_ = cmd.MarkFlagRequired("storage-type")
	_ = cmd.MarkFlagRequired("key-prefix")

	return cmd
}

func run(ctx context.Context, opts options) error {
	if _, err := os.Stat(opts.snapshotPath); err != nil {
		return fmt.Errorf("snapshot file not accessible: %w", err)
	}

	opts.keyPrefix = strings.TrimSuffix(opts.keyPrefix, "/")
	key := fmt.Sprintf("%s/%d%s", opts.keyPrefix, time.Now().Unix(), filepath.Ext(opts.snapshotPath))

	uploader, err := newUploader(ctx, opts)
	if err != nil {
		return err
	}

	result, err := uploader.Upload(ctx, opts.snapshotPath, key)
	if err != nil {
		return fmt.Errorf("failed to upload snapshot: %w", err)
	}

	fmt.Println(result.URL)
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
