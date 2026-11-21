package pki

import (
	"slices"
	"testing"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
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
			wantDNS := "oauth-openshift.dummy.svc.cluster.local"
			if test.expectIP {
				if !slices.Equal(cert.DNSNames, []string{wantDNS}) {
					t.Errorf("expected DNS names [%s], got %v", wantDNS, cert.DNSNames)
				}
				if len(cert.IPAddresses) == 0 {
					t.Errorf("expected cert to have IP addresses, got none")
				}
			} else {
				if !slices.Contains(cert.DNSNames, wantDNS) || !slices.Contains(cert.DNSNames, test.address) {
					t.Errorf("expected DNS names to include %s and %s, got %v", wantDNS, test.address, cert.DNSNames)
				}
				if len(cert.IPAddresses) > 0 {
					t.Errorf("cert has IP addresses, expected none")
				}
			}
		})
	}
}
