package pki

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileRootCAConfigMap(t *testing.T) {
	t.Parallel()

	ownerRef := config.OwnerRef{}

	rootCASecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "root-ca",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			certs.CASignerCertMapKey: []byte("root-ca-cert"),
		},
	}

	tests := []struct {
		name                       string
		observedDefaultIngressCert *corev1.ConfigMap
		namedCertSecrets           []*corev1.Secret
		expectedCACert             string
	}{
		{
			name:           "When no named certs are configured, it should contain only the root CA",
			expectedCACert: "root-ca-cert",
		},
		{
			name: "When an observed default ingress cert exists, it should include root CA and ingress cert",
			observedDefaultIngressCert: &corev1.ConfigMap{
				Data: map[string]string{
					certs.CASignerCertMapKey: "ingress-cert",
				},
			},
			expectedCACert: "root-ca-certingress-cert",
		},
		{
			name: "When one named cert is configured, it should append its tls.crt to the CA bundle",
			namedCertSecrets: []*corev1.Secret{
				{
					Data: map[string][]byte{
						corev1.TLSCertKey: []byte("named-cert-1"),
					},
				},
			},
			expectedCACert: "root-ca-cert\nnamed-cert-1",
		},
		{
			name: "When multiple named certs are configured, it should append all tls.crt to the CA bundle",
			namedCertSecrets: []*corev1.Secret{
				{
					Data: map[string][]byte{
						corev1.TLSCertKey: []byte("named-cert-1"),
					},
				},
				{
					Data: map[string][]byte{
						corev1.TLSCertKey: []byte("named-cert-2"),
					},
				},
			},
			expectedCACert: "root-ca-cert\nnamed-cert-1\nnamed-cert-2",
		},
		{
			name: "When a named cert has empty tls.crt, it should skip it",
			namedCertSecrets: []*corev1.Secret{
				{
					Data: map[string][]byte{
						corev1.TLSCertKey: []byte("named-cert-1"),
					},
				},
				{
					Data: map[string][]byte{
						corev1.TLSCertKey: []byte(""),
					},
				},
			},
			expectedCACert: "root-ca-cert\nnamed-cert-1",
		},
		{
			name: "When a named cert has no tls.crt key, it should skip it",
			namedCertSecrets: []*corev1.Secret{
				{
					Data: map[string][]byte{
						corev1.TLSCertKey: []byte("named-cert-1"),
					},
				},
				{
					Data: map[string][]byte{
						"other-key": []byte("some-data"),
					},
				},
			},
			expectedCACert: "root-ca-cert\nnamed-cert-1",
		},
		{
			name: "When ingress cert and named certs are both configured, it should include all",
			observedDefaultIngressCert: &corev1.ConfigMap{
				Data: map[string]string{
					certs.CASignerCertMapKey: "ingress-cert",
				},
			},
			namedCertSecrets: []*corev1.Secret{
				{
					Data: map[string][]byte{
						corev1.TLSCertKey: []byte("named-cert-1"),
					},
				},
			},
			expectedCACert: "root-ca-certingress-cert\nnamed-cert-1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "root-ca",
					Namespace: "test-ns",
				},
			}

			err := ReconcileRootCAConfigMap(cm, ownerRef, rootCASecret, tc.observedDefaultIngressCert, tc.namedCertSecrets)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cm.Data[certs.CASignerCertMapKey]).To(Equal(tc.expectedCACert))
		})
	}
}
