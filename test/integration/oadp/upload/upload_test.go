//go:build integration
// +build integration

package upload

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	. "github.com/onsi/gomega"
)

const binaryPath = "../../../../bin/control-plane-operator"

// testConfig holds configuration for integration tests.
// These values are read from environment variables.
type testConfig struct {
	// AWS
	AWSBucket          string
	AWSRegion          string
	AWSCredentialsFile string
	AWSKMSKeyARN       string

	// Azure
	AzureContainer      string
	AzureStorageAccount string
	AzureCredentialsFile string
	AzureEncryptionScope string
}

func loadTestConfig(t *testing.T) testConfig {
	t.Helper()
	return testConfig{
		AWSBucket:            os.Getenv("ETCD_UPLOAD_TEST_AWS_BUCKET"),
		AWSRegion:            os.Getenv("ETCD_UPLOAD_TEST_AWS_REGION"),
		AWSCredentialsFile:   os.Getenv("ETCD_UPLOAD_TEST_AWS_CREDENTIALS_FILE"),
		AWSKMSKeyARN:         os.Getenv("ETCD_UPLOAD_TEST_AWS_KMS_KEY_ARN"),
		AzureContainer:       os.Getenv("ETCD_UPLOAD_TEST_AZURE_CONTAINER"),
		AzureStorageAccount:  os.Getenv("ETCD_UPLOAD_TEST_AZURE_STORAGE_ACCOUNT"),
		AzureCredentialsFile: os.Getenv("ETCD_UPLOAD_TEST_AZURE_CREDENTIALS_FILE"),
		AzureEncryptionScope: os.Getenv("ETCD_UPLOAD_TEST_AZURE_ENCRYPTION_SCOPE"),
	}
}

func createTestSnapshot(t *testing.T) string {
	t.Helper()
	return createTestSnapshotWithSize(t, 64*1024) // 64KB fake snapshot
}

func createTestSnapshotWithSize(t *testing.T, sizeBytes int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.db")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create test snapshot: %v", err)
	}
	defer f.Close()
	buf := make([]byte, 1024*1024)
	for i := range buf {
		buf[i] = byte(i % 256)
	}
	for written := 0; written < sizeBytes; {
		chunk := buf
		if remaining := sizeBytes - written; remaining < len(chunk) {
			chunk = chunk[:remaining]
		}
		n, err := f.Write(chunk)
		if err != nil {
			t.Fatalf("failed to write test snapshot: %v", err)
		}
		written += n
	}
	return path
}

func runEtcdUpload(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), binaryPath, append([]string{"etcd-upload"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestEtcdUploadS3(t *testing.T) {
	cfg := loadTestConfig(t)

	tests := []struct {
		name       string
		skip       func() string
		args       func(snapshotPath string) []string
		assertURL  func(g Gomega, url string)
	}{
		{
			name: "When uploading to S3 without KMS it should succeed and return an S3 URL",
			skip: func() string {
				if cfg.AWSBucket == "" || cfg.AWSRegion == "" || cfg.AWSCredentialsFile == "" {
					return "AWS environment variables not set (ETCD_UPLOAD_TEST_AWS_BUCKET, ETCD_UPLOAD_TEST_AWS_REGION, ETCD_UPLOAD_TEST_AWS_CREDENTIALS_FILE)"
				}
				return ""
			},
			args: func(snapshotPath string) []string {
				return []string{
					"--snapshot-path", snapshotPath,
					"--storage-type", "S3",
					"--aws-bucket", cfg.AWSBucket,
					"--aws-region", cfg.AWSRegion,
					"--key-prefix", "integration-test/no-kms",
					"--credentials-file", cfg.AWSCredentialsFile,
				}
			},
			assertURL: func(g Gomega, url string) {
				g.Expect(url).To(HavePrefix("s3://" + cfg.AWSBucket + "/integration-test/no-kms/"))
				g.Expect(url).To(HaveSuffix(".db\n"))
			},
		},
		{
			name: "When uploading to S3 with KMS it should succeed and return an S3 URL",
			skip: func() string {
				if cfg.AWSBucket == "" || cfg.AWSRegion == "" || cfg.AWSCredentialsFile == "" || cfg.AWSKMSKeyARN == "" {
					return "AWS+KMS environment variables not set (ETCD_UPLOAD_TEST_AWS_KMS_KEY_ARN)"
				}
				return ""
			},
			args: func(snapshotPath string) []string {
				return []string{
					"--snapshot-path", snapshotPath,
					"--storage-type", "S3",
					"--aws-bucket", cfg.AWSBucket,
					"--aws-region", cfg.AWSRegion,
					"--key-prefix", "integration-test/with-kms",
					"--credentials-file", cfg.AWSCredentialsFile,
					"--aws-kms-key-arn", cfg.AWSKMSKeyARN,
				}
			},
			assertURL: func(g Gomega, url string) {
				g.Expect(url).To(HavePrefix("s3://" + cfg.AWSBucket + "/integration-test/with-kms/"))
				g.Expect(url).To(HaveSuffix(".db\n"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if reason := tt.skip(); reason != "" {
				t.Skip(reason)
			}
			g := NewGomegaWithT(t)

			snapshotPath := createTestSnapshot(t)
			stdout, stderr, err := runEtcdUpload(t, tt.args(snapshotPath)...)
			g.Expect(err).ToNot(HaveOccurred(), "etcd-upload failed: %s", stderr)
			tt.assertURL(g, stdout)
		})
	}
}

// TestEtcdUploadS3MultipartLargeFile validates that large files (>5MB) are correctly
// uploaded via multipart upload with CRC32 checksum validation.
// This test catches the InvalidPart bug present in Transfer Manager v0.1.0 (pre-GA),
// where per-part CRC32 checksums were not propagated to CompleteMultipartUpload.
// See: https://github.com/aws/aws-sdk-go-v2/issues/3007
func TestEtcdUploadS3MultipartLargeFile(t *testing.T) {
	cfg := loadTestConfig(t)
	if cfg.AWSBucket == "" || cfg.AWSRegion == "" || cfg.AWSCredentialsFile == "" {
		t.Skip("AWS environment variables not set (ETCD_UPLOAD_TEST_AWS_BUCKET, ETCD_UPLOAD_TEST_AWS_REGION, ETCD_UPLOAD_TEST_AWS_CREDENTIALS_FILE)")
	}

	g := NewGomegaWithT(t)

	// 61MB file to force multipart upload (threshold is ~5MB)
	snapshotPath := createTestSnapshotWithSize(t, 61*1024*1024)

	stdout, stderr, err := runEtcdUpload(t,
		"--snapshot-path", snapshotPath,
		"--storage-type", "S3",
		"--aws-bucket", cfg.AWSBucket,
		"--aws-region", cfg.AWSRegion,
		"--key-prefix", "integration-test/multipart",
		"--credentials-file", cfg.AWSCredentialsFile,
	)
	g.Expect(err).ToNot(HaveOccurred(), "etcd-upload multipart failed: %s", stderr)
	g.Expect(stdout).To(HavePrefix("s3://" + cfg.AWSBucket + "/integration-test/multipart/"))
	g.Expect(stdout).To(HaveSuffix(".db\n"))
	t.Logf("Multipart upload succeeded: %s", stdout)

	// Cleanup: delete the uploaded object to avoid storage growth.
	key, err := parseS3Key(stdout, cfg.AWSBucket)
	g.Expect(err).ToNot(HaveOccurred(), "failed to parse S3 key from output")
	deleteS3Object(t, cfg, key)
}

func TestEtcdUploadAzureBlob(t *testing.T) {
	cfg := loadTestConfig(t)

	tests := []struct {
		name       string
		skip       func() string
		args       func(snapshotPath string) []string
		assertURL  func(g Gomega, url string)
	}{
		{
			name: "When uploading to Azure Blob without encryption it should succeed and return an HTTPS URL",
			skip: func() string {
				if cfg.AzureContainer == "" || cfg.AzureStorageAccount == "" || cfg.AzureCredentialsFile == "" {
					return "Azure environment variables not set (ETCD_UPLOAD_TEST_AZURE_CONTAINER, ETCD_UPLOAD_TEST_AZURE_STORAGE_ACCOUNT, ETCD_UPLOAD_TEST_AZURE_CREDENTIALS_FILE)"
				}
				return ""
			},
			args: func(snapshotPath string) []string {
				return []string{
					"--snapshot-path", snapshotPath,
					"--storage-type", "AzureBlob",
					"--azure-container", cfg.AzureContainer,
					"--azure-storage-account", cfg.AzureStorageAccount,
					"--key-prefix", "integration-test/no-encryption",
					"--credentials-file", cfg.AzureCredentialsFile,
				}
			},
			assertURL: func(g Gomega, url string) {
				g.Expect(url).To(HavePrefix("https://" + cfg.AzureStorageAccount + ".blob.core.windows.net/" + cfg.AzureContainer + "/integration-test/no-encryption/"))
				g.Expect(url).To(HaveSuffix(".db\n"))
			},
		},
		{
			name: "When uploading to Azure Blob with encryption scope it should succeed and return an HTTPS URL",
			skip: func() string {
				if cfg.AzureContainer == "" || cfg.AzureStorageAccount == "" || cfg.AzureCredentialsFile == "" || cfg.AzureEncryptionScope == "" {
					return "Azure+encryption environment variables not set (ETCD_UPLOAD_TEST_AZURE_ENCRYPTION_SCOPE)"
				}
				return ""
			},
			args: func(snapshotPath string) []string {
				return []string{
					"--snapshot-path", snapshotPath,
					"--storage-type", "AzureBlob",
					"--azure-container", cfg.AzureContainer,
					"--azure-storage-account", cfg.AzureStorageAccount,
					"--key-prefix", "integration-test/with-encryption",
					"--credentials-file", cfg.AzureCredentialsFile,
					"--azure-encryption-scope", cfg.AzureEncryptionScope,
				}
			},
			assertURL: func(g Gomega, url string) {
				g.Expect(url).To(HavePrefix("https://" + cfg.AzureStorageAccount + ".blob.core.windows.net/" + cfg.AzureContainer + "/integration-test/with-encryption/"))
				g.Expect(url).To(HaveSuffix(".db\n"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if reason := tt.skip(); reason != "" {
				t.Skip(reason)
			}
			g := NewGomegaWithT(t)

			snapshotPath := createTestSnapshot(t)
			stdout, stderr, err := runEtcdUpload(t, tt.args(snapshotPath)...)
			g.Expect(err).ToNot(HaveOccurred(), "etcd-upload failed: %s", stderr)
			tt.assertURL(g, stdout)
		})
	}
}

// parseS3Key extracts the object key from an s3:// URL output by etcd-upload.
// The stdout format is "s3://<bucket>/<key>\n".
func parseS3Key(stdout, bucket string) (string, error) {
	prefix := fmt.Sprintf("s3://%s/", bucket)
	trimmed := strings.TrimSpace(stdout)
	if !strings.HasPrefix(trimmed, prefix) {
		return "", fmt.Errorf("unexpected S3 URL format: %q", trimmed)
	}
	return strings.TrimPrefix(trimmed, prefix), nil
}

// deleteS3Object deletes an S3 object using the test configuration credentials.
func deleteS3Object(t *testing.T, cfg testConfig, key string) {
	t.Helper()
	ctx := context.Background()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.AWSRegion),
		awsconfig.WithSharedCredentialsFiles([]string{cfg.AWSCredentialsFile}),
	)
	if err != nil {
		t.Logf("Warning: failed to load AWS config for cleanup: %v", err)
		return
	}

	s3Client := s3.NewFromConfig(awsCfg)
	_, err = s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(cfg.AWSBucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Logf("Warning: failed to delete S3 object s3://%s/%s: %v", cfg.AWSBucket, key, err)
		return
	}
	t.Logf("Cleaned up S3 object: s3://%s/%s", cfg.AWSBucket, key)
}
