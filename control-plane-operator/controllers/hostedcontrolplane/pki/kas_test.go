package pki

import (
	"crypto/x509/pkix"
	"testing"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/podspec"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

func TestAddBracketsIfIPv6(t *testing.T) {
	tests := []struct {
		name       string
		apiAddress string
		want       string
	}{
		{
			name:       "When given an IPv4 address, it should not add brackets",
			apiAddress: "192.168.1.1",
			want:       "192.168.1.1",
		},
		{
			name:       "When given a URL with port, it should not add brackets",
			apiAddress: "https://test.tld:8451",
			want:       "https://test.tld:8451",
		},
		{
			name:       "When given a URL without port, it should not add brackets",
			apiAddress: "https://test",
			want:       "https://test",
		},
		{
			name:       "When given a hostname with port, it should not add brackets",
			apiAddress: "test.tld:8451",
			want:       "test.tld:8451",
		},
		{
			name:       "When given a simplified IPv6 address, it should return it with brackets",
			apiAddress: "fd00::1",
			want:       "[fd00::1]",
		},
		{
			name:       "When given a full IPv6 address, it should return it with brackets",
			apiAddress: "fd00:0000:0000:0000:0000:0000:1:99",
			want:       "[fd00:0000:0000:0000:0000:0000:1:99]",
		},
		{
			name:       "When given an invalid IPv6 address, it should return it without brackets",
			apiAddress: "fd00:0000:0000:0000:0000:0000:1:99000:00000000000",
			want:       "fd00:0000:0000:0000:0000:0000:1:99000:00000000000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AddBracketsIfIPv6(tt.apiAddress)
			if got != tt.want {
				t.Errorf("AddBracketsIfIPv6() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReconcileServiceAccountKubeconfigWithURL(t *testing.T) {
	t.Parallel()

	caCfg := certs.CertCfg{
		IsCA:    true,
		Subject: pkix.Name{CommonName: "root-ca", OrganizationalUnit: []string{"unit"}},
	}
	caKey, caCert, err := certs.GenerateSelfSignedCertificate(&caCfg)
	if err != nil {
		t.Fatalf("failed to generate CA: %v", err)
	}

	csrSigner := &corev1.Secret{
		Data: map[string][]byte{
			certs.CASignerCertMapKey: certs.CertToPem(caCert),
			certs.CASignerKeyMapKey:  certs.PrivateKeyToPem(caKey),
		},
	}
	caConfigMap := &corev1.ConfigMap{
		Data: map[string]string{
			certs.CASignerCertMapKey: string(certs.CertToPem(caCert)),
		},
	}
	secret := &corev1.Secret{}
	localhostURL := "https://localhost:9443"

	testCases := []struct {
		name string
	}{
		{
			name: "When reconciling service account kubeconfig with explicit URL it should use that URL as cluster server",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ReconcileServiceAccountKubeconfigWithURL(secret, csrSigner, caConfigMap, "openshift-authentication", "azure-workload-identity-webhook", localhostURL); err != nil {
				t.Fatalf("failed to reconcile kubeconfig: %v", err)
			}

			kubeconfigData, hasKubeconfig := secret.Data[podspec.KubeconfigKey]
			if !hasKubeconfig {
				t.Fatalf("expected %q key to be present in secret data", podspec.KubeconfigKey)
			}

			kubeconfig, err := clientcmd.Load(kubeconfigData)
			if err != nil {
				t.Fatalf("failed to parse kubeconfig data: %v", err)
			}
			if kubeconfig.Clusters["cluster"].Server != localhostURL {
				t.Fatalf("expected kubeconfig server %q, got %q", localhostURL, kubeconfig.Clusters["cluster"].Server)
			}
		})
	}
}
