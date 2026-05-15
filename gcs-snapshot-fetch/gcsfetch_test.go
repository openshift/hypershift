package gcsfetch

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

type mockLister struct {
	objects   []string
	listErr   error
	dlErr     error
	dlBucket  string
	dlObject  string
	dlContent []byte
}

func (m *mockLister) ListObjects(ctx context.Context, bucket, prefix string) ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.objects, nil
}

func (m *mockLister) DownloadObject(ctx context.Context, bucket, object, destPath string) error {
	m.dlBucket = bucket
	m.dlObject = object
	if m.dlErr != nil {
		return m.dlErr
	}
	content := m.dlContent
	if content == nil {
		content = []byte("fake-etcd-snapshot-data")
	}
	return os.WriteFile(destPath, content, 0644)
}

func TestFetchLatestSnapshot(t *testing.T) {
	t.Run("When no snapshots exist it should write no-snapshot termination message", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockLister{objects: nil}
		opts := options{gcsBucket: "my-bucket", infraID: "test-infra", outputDir: t.TempDir()}

		err := fetchLatestSnapshotWith(context.Background(), mock, opts)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When snapshots exist it should download the latest one", func(t *testing.T) {
		g := NewGomegaWithT(t)
		outDir := t.TempDir()
		mock := &mockLister{
			objects: []string{
				"test-infra/1000000000.db",
				"test-infra/1000000100.db",
				"test-infra/1000000050.db",
			},
		}
		opts := options{gcsBucket: "my-bucket", infraID: "test-infra", outputDir: outDir}

		err := fetchLatestSnapshotWith(context.Background(), mock, opts)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mock.dlObject).To(Equal("test-infra/1000000100.db"))
		g.Expect(mock.dlBucket).To(Equal("my-bucket"))

		snapshotPath := filepath.Join(outDir, "snapshot.db")
		_, err = os.Stat(snapshotPath)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When listing fails it should return the error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockLister{listErr: fmt.Errorf("permission denied")}
		opts := options{gcsBucket: "my-bucket", infraID: "test-infra", outputDir: t.TempDir()}

		err := fetchLatestSnapshotWith(context.Background(), mock, opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("permission denied"))
	})

	t.Run("When download fails it should return the error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		mock := &mockLister{
			objects: []string{"test-infra/1000000000.db"},
			dlErr:   fmt.Errorf("download failed"),
		}
		opts := options{gcsBucket: "my-bucket", infraID: "test-infra", outputDir: t.TempDir()}

		err := fetchLatestSnapshotWith(context.Background(), mock, opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("download failed"))
	})

	t.Run("When latest object is a tar.gz it should extract snapshot and secrets", func(t *testing.T) {
		g := NewGomegaWithT(t)
		outDir := t.TempDir()

		archiveContent := createTestArchive(t, map[string][]byte{
			"snapshot.db":              []byte("snapshot-data"),
			"secrets/root-ca.json":     []byte(`{"data":{"ca.crt":"Y2VydA=="}}`),
			"secrets/etcd-signer.json": []byte(`{"data":{"ca.key":"a2V5"}}`),
		})

		mock := &mockLister{
			objects:   []string{"test-infra/1000000100.tar.gz"},
			dlContent: archiveContent,
		}
		opts := options{gcsBucket: "my-bucket", infraID: "test-infra", outputDir: outDir}

		err := fetchLatestSnapshotWith(context.Background(), mock, opts)
		g.Expect(err).ToNot(HaveOccurred())

		data, err := os.ReadFile(filepath.Join(outDir, "snapshot.db"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(data)).To(Equal("snapshot-data"))

		data, err = os.ReadFile(filepath.Join(outDir, "secrets", "root-ca.json"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(data)).To(ContainSubstring("ca.crt"))

		data, err = os.ReadFile(filepath.Join(outDir, "secrets", "etcd-signer.json"))
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(data)).To(ContainSubstring("ca.key"))
	})

	t.Run("When tar.gz has no snapshot.db it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		outDir := t.TempDir()

		archiveContent := createTestArchive(t, map[string][]byte{
			"secrets/root-ca.json": []byte(`{"data":{}}`),
		})

		mock := &mockLister{
			objects:   []string{"test-infra/1000000100.tar.gz"},
			dlContent: archiveContent,
		}
		opts := options{gcsBucket: "my-bucket", infraID: "test-infra", outputDir: outDir}

		err := fetchLatestSnapshotWith(context.Background(), mock, opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("snapshot.db"))
	})

	t.Run("When both tar.gz and db exist it should pick the latest by name", func(t *testing.T) {
		g := NewGomegaWithT(t)
		outDir := t.TempDir()

		archiveContent := createTestArchive(t, map[string][]byte{
			"snapshot.db": []byte("archive-snapshot"),
		})

		mock := &mockLister{
			objects: []string{
				"test-infra/1000000050.db",
				"test-infra/1000000100.tar.gz",
			},
			dlContent: archiveContent,
		}
		opts := options{gcsBucket: "my-bucket", infraID: "test-infra", outputDir: outDir}

		err := fetchLatestSnapshotWith(context.Background(), mock, opts)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(mock.dlObject).To(Equal("test-infra/1000000100.tar.gz"))
	})
}

func createTestArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	g := NewGomegaWithT(t)

	tmpFile, err := os.CreateTemp(t.TempDir(), "test-*.tar.gz")
	g.Expect(err).ToNot(HaveOccurred())
	defer tmpFile.Close()

	gw := gzip.NewWriter(tmpFile)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		g.Expect(tw.WriteHeader(hdr)).To(Succeed())
		_, err := tw.Write(content)
		g.Expect(err).ToNot(HaveOccurred())
	}

	g.Expect(tw.Close()).To(Succeed())
	g.Expect(gw.Close()).To(Succeed())

	data, err := os.ReadFile(tmpFile.Name())
	g.Expect(err).ToNot(HaveOccurred())
	return data
}
