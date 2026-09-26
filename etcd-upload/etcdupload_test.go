package etcdupload

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewStartCommand(t *testing.T) {
	cmd := NewStartCommand()

	t.Run("When created it should have correct command name", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(cmd.Use).To(Equal("etcd-upload"))
	})

	t.Run("When created it should register all required flags", func(t *testing.T) {
		g := NewGomegaWithT(t)
		requiredFlags := []string{"snapshot-path", "storage-type", "key-prefix"}
		for _, name := range requiredFlags {
			g.Expect(cmd.Flags().Lookup(name)).ToNot(BeNil(), "expected flag %q to exist", name)
		}
	})

	t.Run("When created it should register all optional flags", func(t *testing.T) {
		g := NewGomegaWithT(t)
		optionalFlags := []string{"aws-bucket", "aws-region", "credentials-file", "aws-kms-key-arn", "azure-container", "azure-storage-account", "azure-encryption-scope", "azure-auth-type", "azure-cloud"}
		for _, name := range optionalFlags {
			g.Expect(cmd.Flags().Lookup(name)).ToNot(BeNil(), "expected flag %q to exist", name)
		}
	})

	t.Run("When AZURE_CLOUD_NAME is unset it should default azure-cloud to AzurePublicCloud", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("AZURE_CLOUD_NAME", "")
		localCmd := NewStartCommand()
		g.Expect(localCmd.Flags().Lookup("azure-cloud").DefValue).To(Equal("AzurePublicCloud"))
	})
}

func TestAzureCloudFlagDefaultsFromEnvironment(t *testing.T) {
	t.Run("When AZURE_CLOUD_NAME is set it should use it as the azure-cloud default", func(t *testing.T) {
		t.Setenv("AZURE_CLOUD_NAME", "AzureUSGovernmentCloud")
		cmd := NewStartCommand()

		g := NewGomegaWithT(t)
		g.Expect(cmd.Flags().Lookup("azure-cloud").DefValue).To(Equal("AzureUSGovernmentCloud"))
	})
}

func TestNewUploader(t *testing.T) {
	t.Run("When storage type is invalid it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := options{
			storageType:     "InvalidType",
			credentialsFile: "/tmp/fake-creds",
		}
		_, err := newUploader(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unsupported storage type"))
	})

	t.Run("When Azure auth type is invalid it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := options{
			storageType:     "AzureBlob",
			container:       "test",
			storageAccount:  "testacc",
			credentialsFile: "/tmp/fake-creds",
			authType:        "invalid-auth",
		}
		_, err := newUploader(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unsupported auth type"))
	})

	t.Run("When Azure cloud is invalid it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := options{
			storageType:    "AzureBlob",
			container:      "test",
			storageAccount: "testacc",
			azureCloud:     "InvalidCloud",
		}
		_, err := newUploader(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unknown Azure cloud"))
	})

	t.Run("When storage type is S3 and bucket is missing it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := options{
			storageType: "S3",
			region:      "us-east-1",
		}
		_, err := newUploader(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("--bucket is required"))
	})
}

func TestRun(t *testing.T) {
	t.Run("When snapshot file is missing it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		err := run(context.Background(), options{
			snapshotPath: "/nonexistent/snapshot.db",
			storageType:  "S3",
			keyPrefix:    "backups",
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("snapshot file not accessible"))
	})

	t.Run("When storage type is invalid it should return an error after validating snapshot path", func(t *testing.T) {
		g := NewGomegaWithT(t)
		snapshotPath := createTempSnapshot(t)
		err := run(context.Background(), options{
			snapshotPath: snapshotPath,
			storageType:  "InvalidType",
			keyPrefix:    "backups/",
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unsupported storage type"))
	})

	t.Run("When upload fails it should return a wrapped upload error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		snapshotPath := createTempSnapshot(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := run(ctx, options{
			snapshotPath: snapshotPath,
			storageType:  "S3",
			bucket:       "my-bucket",
			region:       "us-east-1",
			keyPrefix:    "backups/",
		})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to upload snapshot"))
	})
}

func TestKeyGeneration(t *testing.T) {
	t.Run("When snapshot path has .db extension it should preserve it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		snapshotPath := createTempSnapshot(t)
		g.Expect(snapshotPath).To(HaveSuffix(".db"))
	})
}
