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
	container        string
	storageAccount   string
	encryptionScope string
}

func NewStartCommand() *cobra.Command {
	opts := options{}

	cmd := &cobra.Command{
		Use:          "etcd-upload",
		Short:        "Upload an etcd snapshot to cloud storage",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT)
			defer cancel()

			return run(ctx, opts)
		},
	}

	cmd.Flags().StringVar(&opts.snapshotPath, "snapshot-path", "", "path to the etcd snapshot file to upload")
	cmd.Flags().StringVar(&opts.storageType, "storage-type", "", "cloud storage backend type (S3 or AzureBlob)")
	cmd.Flags().StringVar(&opts.bucket, "bucket", "", "S3 bucket name")
	cmd.Flags().StringVar(&opts.region, "region", "", "AWS region of the S3 bucket")
	cmd.Flags().StringVar(&opts.keyPrefix, "key-prefix", "", "key prefix for the backup file in cloud storage")
	cmd.Flags().StringVar(&opts.credentialsFile, "credentials-file", "", "path to cloud credentials file")
	cmd.Flags().StringVar(&opts.kmsKeyARN, "kms-key-arn", "", "ARN of the AWS KMS key for SSE-KMS encryption (optional)")
	cmd.Flags().StringVar(&opts.container, "container", "", "Azure Blob Storage container name")
	cmd.Flags().StringVar(&opts.storageAccount, "storage-account", "", "Azure Storage Account name")
	cmd.Flags().StringVar(&opts.encryptionScope, "encryption-scope", "", "Azure encryption scope name for server-side encryption (optional)")

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

	uploader, err := newUploader(opts)
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

func newUploader(opts options) (Uploader, error) {
	switch opts.storageType {
	case "S3":
		return NewS3Uploader(opts.bucket, opts.region, opts.credentialsFile, opts.kmsKeyARN)
	case "AzureBlob":
		return NewAzureBlobUploader(opts.container, opts.storageAccount, opts.credentialsFile, opts.encryptionScope)
	default:
		return nil, fmt.Errorf("unsupported storage type: %q (must be S3 or AzureBlob)", opts.storageType)
	}
}
