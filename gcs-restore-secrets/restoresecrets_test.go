package gcsrestoresecrets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func writeSecretJSON(t *testing.T, dir, name string, data map[string]string) {
	t.Helper()
	g := NewWithT(t)
	encoded := make(map[string]string)
	for k, v := range data {
		encoded[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	jsonBytes, err := json.Marshal(secretJSON{Data: encoded})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(os.WriteFile(filepath.Join(dir, name+".json"), jsonBytes, 0644)).To(Succeed())
}

func TestRestoreSecrets(t *testing.T) {
	t.Run("When secrets dir has root-ca and etcd-signer it should create both secrets", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		writeSecretJSON(t, dir, "root-ca", map[string]string{
			"ca.crt": "root-cert",
			"ca.key": "root-key",
		})
		writeSecretJSON(t, dir, "etcd-signer", map[string]string{
			"ca.crt": "signer-cert",
			"ca.key": "signer-key",
		})

		k8sClient := newFakeClient()
		opts := options{secretsDir: dir, namespace: "test-ns"}

		err := restoreSecrets(context.Background(), k8sClient, opts)
		g.Expect(err).ToNot(HaveOccurred())

		secret := &corev1.Secret{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{
			Name: "root-ca", Namespace: "test-ns",
		}, secret)).To(Succeed())
		g.Expect(string(secret.Data["ca.crt"])).To(Equal("root-cert"))
		g.Expect(string(secret.Data["ca.key"])).To(Equal("root-key"))

		g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{
			Name: "etcd-signer", Namespace: "test-ns",
		}, secret)).To(Succeed())
		g.Expect(string(secret.Data["ca.crt"])).To(Equal("signer-cert"))
		g.Expect(string(secret.Data["ca.key"])).To(Equal("signer-key"))
	})

	t.Run("When secrets dir does not exist it should succeed with no-secrets", func(t *testing.T) {
		g := NewWithT(t)
		k8sClient := newFakeClient()
		opts := options{secretsDir: filepath.Join(t.TempDir(), "missing"), namespace: "test-ns"}

		err := restoreSecrets(context.Background(), k8sClient, opts)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When secrets dir is empty it should succeed with no-secrets", func(t *testing.T) {
		g := NewWithT(t)
		k8sClient := newFakeClient()
		opts := options{secretsDir: t.TempDir(), namespace: "test-ns"}

		err := restoreSecrets(context.Background(), k8sClient, opts)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When a secret already exists it should update its data", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		writeSecretJSON(t, dir, "root-ca", map[string]string{
			"ca.crt": "new-cert",
		})

		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "root-ca", Namespace: "test-ns"},
			Data:       map[string][]byte{"ca.crt": []byte("old-cert")},
		}
		k8sClient := newFakeClient(existing)
		opts := options{secretsDir: dir, namespace: "test-ns"}

		err := restoreSecrets(context.Background(), k8sClient, opts)
		g.Expect(err).ToNot(HaveOccurred())

		secret := &corev1.Secret{}
		g.Expect(k8sClient.Get(context.Background(), client.ObjectKey{
			Name: "root-ca", Namespace: "test-ns",
		}, secret)).To(Succeed())
		g.Expect(string(secret.Data["ca.crt"])).To(Equal("new-cert"))
	})

	t.Run("When JSON is malformed it should return an error", func(t *testing.T) {
		g := NewWithT(t)
		dir := t.TempDir()
		g.Expect(os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not-json"), 0644)).To(Succeed())

		k8sClient := newFakeClient()
		opts := options{secretsDir: dir, namespace: "test-ns"}

		err := restoreSecrets(context.Background(), k8sClient, opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("parse"))
	})

	t.Run("When command is created it should register required flags", func(t *testing.T) {
		g := NewWithT(t)
		cmd := NewStartCommand()
		g.Expect(cmd.Flags().Lookup("secrets-dir")).ToNot(BeNil())
		g.Expect(cmd.Flags().Lookup("namespace")).ToNot(BeNil())
	})
}
