package validatesnapshot

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func createTestSnapshot(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "snapshot.db"), []byte("fake-snapshot-data"), 0644); err != nil {
		t.Fatal(err)
	}
}

func createTestSecrets(t *testing.T, dir string) {
	t.Helper()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0755); err != nil {
		t.Fatal(err)
	}
	secret := map[string]interface{}{
		"data": map[string]string{
			"tls.crt": base64.StdEncoding.EncodeToString([]byte("cert-data")),
			"tls.key": base64.StdEncoding.EncodeToString([]byte("key-data")),
		},
	}
	data, _ := json.Marshal(secret)
	if err := os.WriteFile(filepath.Join(secretsDir, "root-ca.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCompleteness(t *testing.T) {
	t.Run("When snapshot.db exists and secrets directory has JSON files it should pass", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		createTestSnapshot(t, dir)
		createTestSecrets(t, dir)

		err := validateCompleteness(dir)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When snapshot.db is missing it should fail with completeness error", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		createTestSecrets(t, dir)

		err := validateCompleteness(dir)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("snapshot.db not found"))
	})

	t.Run("When snapshot.db is empty it should fail with completeness error", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "snapshot.db"), []byte{}, 0644)
		createTestSecrets(t, dir)

		err := validateCompleteness(dir)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("snapshot.db is empty"))
	})

	t.Run("When secrets directory does not exist it should fail with completeness error", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		createTestSnapshot(t, dir)

		err := validateCompleteness(dir)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("secrets directory not found"))
	})

	t.Run("When secrets directory has no JSON files it should fail with completeness error", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		createTestSnapshot(t, dir)
		os.MkdirAll(filepath.Join(dir, "secrets"), 0755)
		os.WriteFile(filepath.Join(dir, "secrets", "not-a-json.txt"), []byte("data"), 0644)

		err := validateCompleteness(dir)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("no .json files found"))
	})
}

func TestValidateSecretFiles(t *testing.T) {
	t.Run("When all secret JSON files have valid data field it should pass", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		secretsDir := filepath.Join(dir, "secrets")
		os.MkdirAll(secretsDir, 0755)

		for _, name := range []string{"root-ca.json", "etcd-signer.json"} {
			secret := map[string]interface{}{
				"data": map[string]string{
					"tls.crt": base64.StdEncoding.EncodeToString([]byte("cert")),
				},
			}
			data, _ := json.Marshal(secret)
			os.WriteFile(filepath.Join(secretsDir, name), data, 0644)
		}

		err := validateSecretFiles(secretsDir)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When a secret JSON file is not valid JSON it should fail with secret-format error", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		secretsDir := filepath.Join(dir, "secrets")
		os.MkdirAll(secretsDir, 0755)
		os.WriteFile(filepath.Join(secretsDir, "bad.json"), []byte("{invalid"), 0644)

		err := validateSecretFiles(secretsDir)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid JSON"))
	})

	t.Run("When a secret JSON file lacks data field it should fail with secret-format error", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		secretsDir := filepath.Join(dir, "secrets")
		os.MkdirAll(secretsDir, 0755)
		data, _ := json.Marshal(map[string]interface{}{"metadata": map[string]string{"name": "test"}})
		os.WriteFile(filepath.Join(secretsDir, "no-data.json"), data, 0644)

		err := validateSecretFiles(secretsDir)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("missing \"data\" field"))
	})

	t.Run("When a secret JSON data value is not valid base64 it should fail with secret-format error", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		secretsDir := filepath.Join(dir, "secrets")
		os.MkdirAll(secretsDir, 0755)
		data, _ := json.Marshal(map[string]interface{}{
			"data": map[string]string{"key": "not-valid-base64!!!"},
		})
		os.WriteFile(filepath.Join(secretsDir, "bad-b64.json"), data, 0644)

		err := validateSecretFiles(secretsDir)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("not valid base64"))
	})

	t.Run("When secrets directory contains non-JSON files it should ignore them", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		secretsDir := filepath.Join(dir, "secrets")
		os.MkdirAll(secretsDir, 0755)

		secret := map[string]interface{}{
			"data": map[string]string{"key": base64.StdEncoding.EncodeToString([]byte("val"))},
		}
		data, _ := json.Marshal(secret)
		os.WriteFile(filepath.Join(secretsDir, "valid.json"), data, 0644)
		os.WriteFile(filepath.Join(secretsDir, "readme.txt"), []byte("not json"), 0644)

		err := validateSecretFiles(secretsDir)
		g.Expect(err).ToNot(HaveOccurred())
	})
}

func TestValidateDigest(t *testing.T) {
	t.Run("When .expected-sha256 matches computed digest it should pass", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		archiveContent := []byte("test-archive-content")
		archivePath := filepath.Join(dir, ".archive.tar.gz")
		os.WriteFile(archivePath, archiveContent, 0644)

		h := sha256.Sum256(archiveContent)
		digest := hex.EncodeToString(h[:])
		os.WriteFile(filepath.Join(dir, ".expected-sha256"), []byte(digest), 0644)

		err := validateDigest(dir)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When .expected-sha256 does not match computed digest it should fail with digest-mismatch", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		os.WriteFile(filepath.Join(dir, ".archive.tar.gz"), []byte("content"), 0644)
		os.WriteFile(filepath.Join(dir, ".expected-sha256"), []byte("0000000000000000000000000000000000000000000000000000000000000000"), 0644)

		err := validateDigest(dir)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("digest mismatch"))
	})

	t.Run("When .expected-sha256 does not exist it should skip digest check", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()

		err := validateDigest(dir)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When .expected-sha256 is empty it should skip digest check", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".expected-sha256"), []byte("  \n"), 0644)

		err := validateDigest(dir)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When .archive.tar.gz does not exist but .expected-sha256 does it should fail", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ".expected-sha256"), []byte("abcdef1234567890"), 0644)

		err := validateDigest(dir)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to compute archive digest"))
	})
}
