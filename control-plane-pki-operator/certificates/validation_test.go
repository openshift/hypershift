package certificates

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net"
	"net/url"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidator(t *testing.T) {
	for signer, testCases := range map[SignerClass][]struct {
		name        string
		csr         *certificatesv1.CertificateSigningRequest
		x509cr      *x509.CertificateRequest
		expectedErr bool
	}{
		CustomerBreakGlassSigner: {
			{
				name: "invalid signer domain",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "invalid",
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:customer-break-glass:user"},
				},
				expectedErr: true,
			},
			{
				name: "invalid signer class",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/other",
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:customer-break-glass:user"},
				},
				expectedErr: true,
			},
			{
				name: "missing required usage",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:customer-break-glass:user"},
				},
				expectedErr: true,
			},
			{
				name: "invalid usage",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageEmailProtection},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:customer-break-glass:user"},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: SAN: dns names specified",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject:  pkix.Name{CommonName: "system:customer-break-glass:user"},
					DNSNames: []string{"example.com"},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: SAN: email addresses specified",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject:        pkix.Name{CommonName: "system:customer-break-glass:user"},
					EmailAddresses: []string{"someone@example.com"},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: SAN: ip addresses specified",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject:     pkix.Name{CommonName: "system:customer-break-glass:user"},
					IPAddresses: []net.IP{[]byte(`127.0.0.1`)},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: SAN: URIs specified",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:customer-break-glass:user"},
					URIs:    []*url.URL{{Scheme: "https"}},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: common name without correct prefix",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "something"},
				},
				expectedErr: true,
			},
			{
				name: "valid: client auth",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:customer-break-glass:user"},
				},
			},
			{
				name: "valid: client auth with extras",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth, certificatesv1.UsageDigitalSignature, certificatesv1.UsageKeyEncipherment},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:customer-break-glass:user"},
				},
			},
		},
		SREBreakGlassSigner: {
			{
				name: "invalid signer domain",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "invalid",
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:sre-break-glass:user"},
				},
				expectedErr: true,
			},
			{
				name: "invalid signer class",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/other",
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:sre-break-glass:user"},
				},
				expectedErr: true,
			},
			{
				name: "missing required usage",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:sre-break-glass:user"},
				},
				expectedErr: true,
			},
			{
				name: "invalid usage",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageEmailProtection},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:sre-break-glass:user"},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: SAN: dns names specified",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject:  pkix.Name{CommonName: "system:sre-break-glass:user"},
					DNSNames: []string{"example.com"},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: SAN: email addresses specified",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject:        pkix.Name{CommonName: "system:sre-break-glass:user"},
					EmailAddresses: []string{"someone@example.com"},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: SAN: ip addresses specified",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject:     pkix.Name{CommonName: "system:sre-break-glass:user"},
					IPAddresses: []net.IP{[]byte(`127.0.0.1`)},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: SAN: URIs specified",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:sre-break-glass:user"},
					URIs:    []*url.URL{{Scheme: "https"}},
				},
				expectedErr: true,
			},
			{
				name: "invalid request: common name without correct prefix",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "something"},
				},
				expectedErr: true,
			},
			{
				name: "valid: client auth",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:sre-break-glass:user"},
				},
			},
			{
				name: "valid: client auth with extras",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.sre-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth, certificatesv1.UsageDigitalSignature, certificatesv1.UsageKeyEncipherment},
					},
				},
				x509cr: &x509.CertificateRequest{
					Subject: pkix.Name{CommonName: "system:sre-break-glass:user"},
				},
			},
		},
	} {
		for _, testCase := range testCases {
			t.Run(fmt.Sprintf("%s.%s", signer, testCase.name), func(t *testing.T) {
				validationErr := Validator(&hypershiftv1beta1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "hc-namespace-hc-name",
						Name:      "hcp-name",
					},
				}, signer)(testCase.csr, testCase.x509cr)
				if testCase.expectedErr && validationErr == nil {
					t.Errorf("expected an error but got none")
				} else if !testCase.expectedErr && validationErr != nil {
					t.Errorf("expected no error but got: %v", validationErr)
				}
			})
		}
	}
}
