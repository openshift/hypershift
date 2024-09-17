package etcdbackup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/spf13/cobra"
)

const (
	DefaultEtcdClientTimeout = 5 * time.Minute
)

type options struct {
	backupDir string

	etcdEndpoint       string
	etcdClientCertFile string
	etcdClientKeyFile  string
	etcdCAFile         string

	s3BucketName   string
	s3BucketRegion string
	s3KeyPrefix    string
	s3ObjectTags   map[string]string

	snapshotFilePath string
}

func NewStartCommand() *cobra.Command {
	opts := options{
		backupDir:          "/tmp",
		etcdClientCertFile: "/etc/etcd/tls/client/etcd-client.crt",
		etcdClientKeyFile:  "/etc/etcd/tls/client/etcd-client.key",
		etcdCAFile:         "/etc/etcd/tls/etcd-ca/ca.crt",
	}

	cmd := &cobra.Command{
		Use:          "etcd-backup",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT)
			defer cancel()

			return run(ctx, opts)
		},
	}

	cmd.Flags().StringVar(&opts.backupDir, "backup-dir", "", "the directory where etcd snapshots are stored")
	cmd.Flags().StringVar(&opts.etcdEndpoint, "etcd-endpoint", "", "endpoint of the etcd cluster to backup.")
	cmd.Flags().StringVar(&opts.etcdClientCertFile, "etcd-client-cert", "", "etcd client cert file.")
	cmd.Flags().StringVar(&opts.etcdClientKeyFile, "etcd-client-key", "", "etcd client cert key file.")
	cmd.Flags().StringVar(&opts.etcdCAFile, "etcd-ca-cert", "", "etcd trusted CA cert file.")
	cmd.Flags().StringVar(&opts.s3BucketName, "s3-bucket-name", "", "name of the S3 bucket to store etcd backups.")
	cmd.Flags().StringVar(&opts.s3BucketRegion, "s3-bucket-region", "", "AWS region of the S3 bucket to store etcd backups.")
	cmd.Flags().StringVar(&opts.s3KeyPrefix, "s3-key-prefix", "", "S3 snapshot key prefix.")
	cmd.Flags().StringToStringVar(&opts.s3ObjectTags, "s3-object-tags", opts.s3ObjectTags, "S3 snapshot object tags.")

	_ = cmd.MarkFlagRequired("etcd-endpoint")
	_ = cmd.MarkFlagRequired("s3-bucket-name")
	_ = cmd.MarkFlagRequired("s3-key-prefix")

	return cmd
}

func run(ctx context.Context, opts options) error {
	filePath := filepath.Join(opts.backupDir, "snapshot.db")
	args := []string{
		"--endpoints",
		opts.etcdEndpoint,
		"--cacert",
		opts.etcdCAFile,
		"--cert",
		opts.etcdClientCertFile,
		"--key",
		opts.etcdClientKeyFile,
		"snapshot",
		"save",
		filePath,
	}

	timeoutContext, cancel := context.WithTimeout(ctx, DefaultEtcdClientTimeout)
	defer cancel()

	localCmd := exec.CommandContext(timeoutContext, "/usr/bin/etcdctl", args...)
	localCmd.Env = append(localCmd.Env, "ETCDCTL_API=3")
	if err := localCmd.Run(); err != nil {
		return fmt.Errorf("failed to snapshot etcd: %w", err)
	}

	opts.snapshotFilePath = filePath
	return uploadToS3(ctx, opts)
}

func uploadToS3(ctx context.Context, opts options) error {
	config := aws.NewConfig()
	// AWS_REGION must be set if s3BucketRegion is empty
	if opts.s3BucketRegion != "" {
		config.Region = aws.String(opts.s3BucketRegion)
	}
	awsSession := session.Must(session.NewSession(config))

	f, err := os.Open(opts.snapshotFilePath)
	if err != nil {
		return fmt.Errorf("failed to open file %q, %v", opts.snapshotFilePath, err)
	}

	opts.s3KeyPrefix = strings.TrimSuffix(opts.s3KeyPrefix, "/")
	key := fmt.Sprintf("%s/%d.db", opts.s3KeyPrefix, time.Now().Unix())

	uploader := s3manager.NewUploader(awsSession, s3manager.WithUploaderRequestOptions())
	output, err := uploader.UploadWithContext(ctx, &s3manager.UploadInput{
		Bucket:  aws.String(opts.s3BucketName),
		Key:     aws.String(key),
		Body:    f,
		Tagging: mapToTags(opts.s3ObjectTags),
	})

	if err != nil {
		return fmt.Errorf("failed to upload snapshot file: %w", err)
	}

	fmt.Printf("snapshot successfully uploaded to %s\n", output.Location)
	return nil
}

func mapToTags(m map[string]string) *string {
	output := ""
	for key, value := range m {
		output += fmt.Sprintf("%s=%s&", key, value)
	}

	return &output
}
