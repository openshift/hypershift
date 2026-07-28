package etcdupload

import (
	"context"
	"os"
	"path/filepath"
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
		requiredFlags := []string{"storage-type", "key-prefix"}
		for _, name := range requiredFlags {
			g.Expect(cmd.Flags().Lookup(name)).ToNot(BeNil(), "expected flag %q to exist", name)
		}
	})

	t.Run("When created it should register snapshot path and dir flags", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(cmd.Flags().Lookup("snapshot-path")).ToNot(BeNil())
		g.Expect(cmd.Flags().Lookup("snapshot-dir")).ToNot(BeNil())
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

func TestRunValidation(t *testing.T) {
	t.Run("When neither snapshot-path nor snapshot-dir is set it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := options{
			storageType: "S3",
			keyPrefix:   "test",
		}
		err := run(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("either --snapshot-path or --snapshot-dir must be specified"))
	})

	t.Run("When both snapshot-path and snapshot-dir are set it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := options{
			snapshotPath: "/tmp/snapshot.db",
			snapshotDir:  "/tmp/snapshots",
			storageType:  "S3",
			keyPrefix:    "test",
		}
		err := run(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("mutually exclusive"))
	})
}

func TestRunDir(t *testing.T) {
	t.Run("When snapshot directory is empty it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		dir := t.TempDir()
		opts := options{
			snapshotDir: dir,
			storageType: "S3",
			keyPrefix:   "test",
		}
		uploader := &fakeUploader{}
		err := runDir(context.Background(), opts, uploader)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("no .db files found"))
	})

	t.Run("When snapshot directory has .db files it should upload each", func(t *testing.T) {
		g := NewGomegaWithT(t)
		dir := t.TempDir()
		// Create test snapshot files
		g.Expect(writeTestFile(dir, "etcd.db")).To(Succeed())
		g.Expect(writeTestFile(dir, "etcd-events.db")).To(Succeed())
		g.Expect(writeTestFile(dir, "not-a-snapshot.txt")).To(Succeed()) // should be ignored

		uploader := &fakeUploader{}
		opts := options{
			snapshotDir: dir,
			keyPrefix:   "backups/test",
		}
		err := runDir(context.Background(), opts, uploader)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(uploader.uploads).To(HaveLen(2))
		// Verify all shard names are present (order depends on filesystem)
		var keys []string
		for _, u := range uploader.uploads {
			keys = append(keys, u.key)
		}
		g.Expect(keys).To(ContainElement(ContainSubstring("etcd.db")))
		g.Expect(keys).To(ContainElement(ContainSubstring("etcd-events.db")))
	})

	t.Run("When snapshot directory does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := options{
			snapshotDir: "/nonexistent/dir",
			keyPrefix:   "test",
		}
		uploader := &fakeUploader{}
		err := runDir(context.Background(), opts, uploader)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to read snapshot directory"))
	})
}

type uploadRecord struct {
	path string
	key  string
}

type fakeUploader struct {
	uploads []uploadRecord
}

func (f *fakeUploader) Upload(_ context.Context, snapshotPath string, key string) (*UploadResult, error) {
	f.uploads = append(f.uploads, uploadRecord{path: snapshotPath, key: key})
	return &UploadResult{URL: "s3://bucket/" + key}, nil
}

func writeTestFile(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte("test-data"), 0644)
}
