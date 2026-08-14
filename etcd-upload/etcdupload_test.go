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
		optionalFlags := []string{"aws-bucket", "aws-region", "credentials-file", "aws-kms-key-arn", "azure-container", "azure-storage-account", "azure-encryption-scope", "azure-auth-type"}
		for _, name := range optionalFlags {
			g.Expect(cmd.Flags().Lookup(name)).ToNot(BeNil(), "expected flag %q to exist", name)
		}
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
}

func TestKeyGeneration(t *testing.T) {
	t.Run("When snapshot path has .db extension it should preserve it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		snapshotPath := createTempSnapshot(t)
		g.Expect(snapshotPath).To(HaveSuffix(".db"))
	})
}
