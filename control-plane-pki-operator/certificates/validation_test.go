package certificates

import (
	"crypto/x509"
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
				expectedErr: true,
			},
			{
				name: "invalid signer class",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/other",
					},
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
					URIs: []*url.URL{{Scheme: "https"}},
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
				x509cr: &x509.CertificateRequest{},
			},
			{
				name: "valid: client auth with extras",
				csr: &certificatesv1.CertificateSigningRequest{
					Spec: certificatesv1.CertificateSigningRequestSpec{
						SignerName: "hypershift.openshift.io/hc-namespace-hc-name.customer-break-glass",
						Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth, certificatesv1.UsageDigitalSignature, certificatesv1.UsageKeyEncipherment},
					},
				},
				x509cr: &x509.CertificateRequest{},
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
