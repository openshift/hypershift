package etcdbackup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFetchAndWriteCerts(t *testing.T) {
	hcpNamespace := "hcp-test"
	etcdClientSecretName := manifests.EtcdClientSecret("").Name
	etcdCAConfigMapName := manifests.EtcdSignerCAConfigMap("").Name

	fullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      etcdClientSecretName,
			Namespace: hcpNamespace,
		},
		Data: map[string][]byte{
			pki.EtcdClientCrtKey: []byte("fake-cert-data"),
			pki.EtcdClientKeyKey: []byte("fake-key-data"),
		},
	}

	fullCAConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      etcdCAConfigMapName,
			Namespace: hcpNamespace,
		},
		Data: map[string]string{
			certs.CASignerCertMapKey: "fake-ca-data",
		},
	}

	tests := []struct {
		name          string
		objects       []crclient.Object
		outputDir     func(t *testing.T) string
		expectErr     bool
		errSubstring  string
		expectedFiles map[string]string
	}{
		{
			name:      "When all resources exist it should write all cert files",
			objects:   []crclient.Object{fullSecret, fullCAConfigMap},
			outputDir: func(t *testing.T) string { return t.TempDir() },
			expectedFiles: map[string]string{
				pki.EtcdClientCrtKey:     "fake-cert-data",
				pki.EtcdClientKeyKey:     "fake-key-data",
				certs.CASignerCertMapKey: "fake-ca-data",
			},
		},
		{
			name:      "When output directory does not exist it should create it and write files",
			objects:   []crclient.Object{fullSecret, fullCAConfigMap},
			outputDir: func(t *testing.T) string { return filepath.Join(t.TempDir(), "nested", "certs") },
			expectedFiles: map[string]string{
				pki.EtcdClientCrtKey:     "fake-cert-data",
				pki.EtcdClientKeyKey:     "fake-key-data",
				certs.CASignerCertMapKey: "fake-ca-data",
			},
		},
		{
			name:         "When etcd-client-tls secret is missing it should return an error",
			objects:      []crclient.Object{fullCAConfigMap},
			outputDir:    func(t *testing.T) string { return t.TempDir() },
			expectErr:    true,
			errSubstring: "failed to get etcd client TLS secret",
		},
		{
			name:         "When etcd-ca configmap is missing it should return an error",
			objects:      []crclient.Object{fullSecret},
			outputDir:    func(t *testing.T) string { return t.TempDir() },
			expectErr:    true,
			errSubstring: "failed to get etcd CA configmap",
		},
		{
			name: "When etcd-client.crt is missing from the secret it should return an error",
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      etcdClientSecretName,
						Namespace: hcpNamespace,
					},
					Data: map[string][]byte{
						pki.EtcdClientKeyKey: []byte("fake-key-data"),
					},
				},
				fullCAConfigMap,
			},
			outputDir:    func(t *testing.T) string { return t.TempDir() },
			expectErr:    true,
			errSubstring: "missing key",
		},
		{
			name: "When etcd-client.key is missing from the secret it should return an error",
			objects: []crclient.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      etcdClientSecretName,
						Namespace: hcpNamespace,
					},
					Data: map[string][]byte{
						pki.EtcdClientCrtKey: []byte("fake-cert-data"),
					},
				},
				fullCAConfigMap,
			},
			outputDir:    func(t *testing.T) string { return t.TempDir() },
			expectErr:    true,
			errSubstring: "missing key",
		},
		{
			name: "When ca.crt is missing from the configmap it should return an error",
			objects: []crclient.Object{
				fullSecret,
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      etcdCAConfigMapName,
						Namespace: hcpNamespace,
					},
					Data: map[string]string{},
				},
			},
			outputDir:    func(t *testing.T) string { return t.TempDir() },
			expectErr:    true,
			errSubstring: "missing key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				Build()

			outputDir := tc.outputDir(t)
			opts := fetchCertsOptions{
				hcpNamespace:     hcpNamespace,
				outputDir:        outputDir,
				etcdClientSecret: etcdClientSecretName,
				etcdCAConfigMap:  etcdCAConfigMapName,
			}

			err := fetchAndWriteCerts(context.Background(), k8sClient, opts)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.errSubstring))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			for name, expectedContent := range tc.expectedFiles {
				path := filepath.Join(outputDir, name)
				data, err := os.ReadFile(path)
				g.Expect(err).ToNot(HaveOccurred(), "failed to read %s", name)
				g.Expect(string(data)).To(Equal(expectedContent))

				info, err := os.Stat(path)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)), "file %s should have 0600 permissions", name)
			}
		})
	}
}

func TestNewFetchCertsCommand(t *testing.T) {
	g := NewWithT(t)
	cmd := NewFetchCertsCommand()

	g.Expect(cmd.Use).To(Equal("fetch-etcd-certs"))

	for _, flag := range []string{"hcp-namespace", "output-dir", "etcd-client-secret", "etcd-ca-configmap"} {
		g.Expect(cmd.Flags().Lookup(flag)).ToNot(BeNil(), "expected flag %q to exist", flag)
	}

	// hcp-namespace should be required
	err := cmd.ValidateRequiredFlags()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("hcp-namespace"))
}
