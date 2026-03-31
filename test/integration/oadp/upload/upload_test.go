//go:build integration
// +build integration

package upload

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.db")
	data := make([]byte, 64*1024) // 64KB fake snapshot
	for i := range data {
		data[i] = byte(i % 256)
	}
	err := os.WriteFile(path, data, 0644)
	if err != nil {
		t.Fatalf("failed to create test snapshot: %v", err)
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
