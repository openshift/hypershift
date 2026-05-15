package etcdupload

import (
	"archive/tar"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestBundleArchive(t *testing.T) {
	t.Run("When snapshot and secrets dirs exist it should create a valid tar.gz", func(t *testing.T) {
		g := NewWithT(t)
		tmpDir := t.TempDir()

		snapshotPath := filepath.Join(tmpDir, "snapshot.db")
		g.Expect(os.WriteFile(snapshotPath, []byte("fake-snapshot-data"), 0644)).To(Succeed())

		secretsDir := filepath.Join(tmpDir, "secrets")
		g.Expect(os.MkdirAll(filepath.Join(secretsDir, "root-ca"), 0755)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(secretsDir, "root-ca", "ca.crt"), []byte("root-cert"), 0644)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(secretsDir, "root-ca", "ca.key"), []byte("root-key"), 0644)).To(Succeed())

		g.Expect(os.MkdirAll(filepath.Join(secretsDir, "etcd-signer"), 0755)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(secretsDir, "etcd-signer", "ca.crt"), []byte("signer-cert"), 0644)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(secretsDir, "etcd-signer", "ca.key"), []byte("signer-key"), 0644)).To(Succeed())

		archivePath, err := bundleArchive(snapshotPath, secretsDir)
		g.Expect(err).ToNot(HaveOccurred())
		defer os.Remove(archivePath)

		files := extractTestArchive(t, archivePath)
		g.Expect(files).To(HaveKey("snapshot.db"))
		g.Expect(string(files["snapshot.db"])).To(Equal("fake-snapshot-data"))
		g.Expect(files).To(HaveKey("secrets/root-ca.json"))
		g.Expect(files).To(HaveKey("secrets/etcd-signer.json"))

		var rootCA secretData
		g.Expect(json.Unmarshal(files["secrets/root-ca.json"], &rootCA)).To(Succeed())
		decoded, err := base64.StdEncoding.DecodeString(rootCA.Data["ca.crt"])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(decoded)).To(Equal("root-cert"))
		decoded, err = base64.StdEncoding.DecodeString(rootCA.Data["ca.key"])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(decoded)).To(Equal("root-key"))
	})

	t.Run("When secrets dir has no subdirectories it should create archive with only snapshot", func(t *testing.T) {
		g := NewWithT(t)
		tmpDir := t.TempDir()

		snapshotPath := filepath.Join(tmpDir, "snapshot.db")
		g.Expect(os.WriteFile(snapshotPath, []byte("data"), 0644)).To(Succeed())

		secretsDir := filepath.Join(tmpDir, "secrets")
		g.Expect(os.MkdirAll(secretsDir, 0755)).To(Succeed())

		archivePath, err := bundleArchive(snapshotPath, secretsDir)
		g.Expect(err).ToNot(HaveOccurred())
		defer os.Remove(archivePath)

		files := extractTestArchive(t, archivePath)
		g.Expect(files).To(HaveLen(1))
		g.Expect(files).To(HaveKey("snapshot.db"))
	})

	t.Run("When snapshot file does not exist it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		tmpDir := t.TempDir()

		secretsDir := filepath.Join(tmpDir, "secrets")
		g.Expect(os.MkdirAll(secretsDir, 0755)).To(Succeed())

		_, err := bundleArchive(filepath.Join(tmpDir, "missing.db"), secretsDir)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("When secrets dir does not exist it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		tmpDir := t.TempDir()

		snapshotPath := filepath.Join(tmpDir, "snapshot.db")
		g.Expect(os.WriteFile(snapshotPath, []byte("data"), 0644)).To(Succeed())

		_, err := bundleArchive(snapshotPath, filepath.Join(tmpDir, "missing"))
		g.Expect(err).To(HaveOccurred())
	})
}

func TestSerializeSecretDir(t *testing.T) {
	t.Run("When directory has ca.crt and ca.key it should produce valid JSON with base64 data", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		g.Expect(os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("my-cert"), 0644)).To(Succeed())
		g.Expect(os.WriteFile(filepath.Join(dir, "ca.key"), []byte("my-key"), 0644)).To(Succeed())

		jsonBytes, err := serializeSecretDir(dir)
		g.Expect(err).ToNot(HaveOccurred())

		var sd secretData
		g.Expect(json.Unmarshal(jsonBytes, &sd)).To(Succeed())
		g.Expect(sd.Data).To(HaveLen(2))

		decoded, err := base64.StdEncoding.DecodeString(sd.Data["ca.crt"])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(decoded)).To(Equal("my-cert"))

		decoded, err = base64.StdEncoding.DecodeString(sd.Data["ca.key"])
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(string(decoded)).To(Equal("my-key"))
	})

	t.Run("When directory is empty it should produce JSON with empty data", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		jsonBytes, err := serializeSecretDir(dir)
		g.Expect(err).ToNot(HaveOccurred())

		var sd secretData
		g.Expect(json.Unmarshal(jsonBytes, &sd)).To(Succeed())
		g.Expect(sd.Data).To(BeEmpty())
	})
}

func extractTestArchive(t *testing.T, archivePath string) map[string][]byte {
	t.Helper()
	g := NewWithT(t)
	f, err := os.Open(archivePath)
	g.Expect(err).ToNot(HaveOccurred())
	defer f.Close()

	gr, err := gzip.NewReader(f)
	g.Expect(err).ToNot(HaveOccurred())
	defer gr.Close()

	tr := tar.NewReader(gr)
	files := make(map[string][]byte)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		g.Expect(err).ToNot(HaveOccurred())
		data, err := io.ReadAll(tr)
		g.Expect(err).ToNot(HaveOccurred())
		files[hdr.Name] = data
	}
	return files
}
