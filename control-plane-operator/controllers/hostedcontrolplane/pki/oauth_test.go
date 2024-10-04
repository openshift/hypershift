package pki

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
)

func TestReconcileOAuthServerCert(t *testing.T) {

	tests := []struct {
		name     string
		address  string
		expectIP bool
	}{
		{
			name:     "host name",
			address:  "www.example.com",
			expectIP: false,
		},
		{
			name:     "numeric ip",
			address:  "100.100.100.1",
			expectIP: true,
		},
	}

	ownerRef := config.OwnerRef{
		Reference: &metav1.OwnerReference{
			APIVersion: "v1",
			Kind:       "Deployment",
			Name:       "dummy",
			UID:        types.UID("12345abcdef"),
			Controller: ptr.To(true),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ca := &corev1.Secret{}
			ca.Name = "test-ca"
			ca.Namespace = "dummy"
			err := reconcileSelfSignedCA(ca, ownerRef, "foo", "bar")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			secret := &corev1.Secret{}
			secret.Name = "cert"
			secret.Namespace = "dummy"
			err = ReconcileOAuthServerCert(secret, ca, ownerRef, test.address)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cert, err := certs.PemToCertificate(secret.Data[corev1.TLSCertKey])
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.expectIP {
				if len(cert.DNSNames) > 0 {
					t.Errorf("cert has dns names, none expected")
				}
				if len(cert.IPAddresses) == 0 {
					t.Errorf("expected cert to have IP addresses, got none")
				}
			} else {
				if len(cert.DNSNames) == 0 {
					t.Errorf("expected cert to have DNS names, got none")
				}
				if len(cert.IPAddresses) > 0 {
					t.Errorf("cert has IP addresses, expected none")
				}
			}
		})
	}
}
