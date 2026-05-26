package util

import (
	"crypto/x509"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/certs"

	"k8s.io/utils/ptr"
)

func TestAllowedCIDRsTargetService(t *testing.T) {
	const ns = "test-hcp"

	publicHC := func(platform hyperv1.PlatformType, svcType hyperv1.PublishingStrategyType) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{Type: platform},
				Services: []hyperv1.ServicePublishingStrategyMapping{{
					Service:                   hyperv1.APIServer,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: svcType},
				}},
			},
		}
		switch platform {
		case hyperv1.AWSPlatform:
			hc.Spec.Platform.AWS = ptr.To(hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public})
		case hyperv1.AzurePlatform:
			hc.Spec.Platform.Azure = ptr.To(hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPublic})
		}
		return hc
	}

	tests := []struct {
		name     string
		hc       *hyperv1.HostedCluster
		aroHCP   bool
		wantName string
		wantNil  bool
	}{
		{
			name:     "When Route strategy on AWS it should return the router service",
			hc:       publicHC(hyperv1.AWSPlatform, hyperv1.Route),
			wantName: "router",
		},
		{
			name:     "When Route strategy on Azure self-managed it should return the router service",
			hc:       publicHC(hyperv1.AzurePlatform, hyperv1.Route),
			wantName: "router",
		},
		{
			name:    "When Route strategy on ARO HCP it should return nil",
			hc:      publicHC(hyperv1.AzurePlatform, hyperv1.Route),
			aroHCP:  true,
			wantNil: true,
		},
		{
			name:     "When LoadBalancer strategy on Azure it should return the Azure LB service",
			hc:       publicHC(hyperv1.AzurePlatform, hyperv1.LoadBalancer),
			wantName: "kube-apiserverlb",
		},
		{
			name: "When LoadBalancer strategy with Azure management annotation it should return the Azure LB service",
			hc: func() *hyperv1.HostedCluster {
				hc := publicHC(hyperv1.NonePlatform, hyperv1.LoadBalancer)
				hc.Annotations = map[string]string{
					hyperv1.ManagementPlatformAnnotation: string(hyperv1.AzurePlatform),
				}
				return hc
			}(),
			wantName: "kube-apiserverlb",
		},
		{
			name:     "When LoadBalancer strategy on AWS it should return the KAS service",
			hc:       publicHC(hyperv1.AWSPlatform, hyperv1.LoadBalancer),
			wantName: "kube-apiserver",
		},
		{
			name: "When private Azure cluster it should return nil",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type:  hyperv1.AzurePlatform,
						Azure: ptr.To(hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPrivate}),
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{{
						Service:                   hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
					}},
				},
			},
			wantNil: true,
		},
		{
			name:    "When NodePort strategy it should return nil",
			hc:      publicHC(hyperv1.AWSPlatform, hyperv1.NodePort),
			wantNil: true,
		},
		{
			name: "When no APIServer strategy it should return nil",
			hc: func() *hyperv1.HostedCluster {
				hc := publicHC(hyperv1.AWSPlatform, hyperv1.Route)
				hc.Spec.Services = nil
				return hc
			}(),
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			if tc.aroHCP {
				azureutil.SetAsAroHCPTest(t)
			}
			svc := allowedCIDRsTargetService(tc.hc, ns)
			if tc.wantNil {
				g.Expect(svc).To(BeNil())
			} else {
				g.Expect(svc).ToNot(BeNil())
				g.Expect(svc.Name).To(Equal(tc.wantName))
				g.Expect(svc.Namespace).To(Equal(ns))
			}
		})
	}
}

// TestGenerateCustomCertificate verifies that our certificate generation works correctly
func TestGenerateCustomCertificate(t *testing.T) {
	testsCases := []struct {
		name       string
		dnsNames   []string
		duration   time.Duration
		wantErr    bool
		expectedCN string
	}{
		{
			name:       "When generating a certificate with DNS names it should succeed",
			dnsNames:   []string{"example.com", "test.example.com"},
			duration:   24 * time.Hour,
			wantErr:    false,
			expectedCN: "example.com",
		},
		{
			name:     "When generating a certificate with no DNS names it should fail",
			dnsNames: []string{},
			duration: 24 * time.Hour,
			wantErr:  true,
		},
		{
			name:       "When generating a certificate with zero duration it should succeed",
			dnsNames:   []string{"example.com"},
			duration:   0,
			wantErr:    false,
			expectedCN: "example.com",
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			certPEM, keyPEM, err := GenerateCustomCertificate(tc.dnsNames, tc.duration)

			if tc.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(certPEM).NotTo(BeNil())
			g.Expect(keyPEM).NotTo(BeNil())

			// Parse the certificate to verify its contents
			cert, err := certs.PemToCertificate(certPEM)
			g.Expect(err).NotTo(HaveOccurred())

			// Verify CommonName
			g.Expect(cert.Subject.CommonName).To(Equal(tc.expectedCN))

			// Verify DNS names
			if len(tc.dnsNames) == 0 {
				g.Expect(cert.DNSNames).To(BeEmpty())
			} else {
				g.Expect(cert.DNSNames).To(Equal(tc.dnsNames))
			}

			// Verify validity period
			if tc.duration > 0 {
				g.Expect(cert.NotAfter.Sub(cert.NotBefore)).To(Equal(tc.duration))
			}

			// Verify key usage
			g.Expect(cert.KeyUsage & x509.KeyUsageKeyEncipherment).NotTo(BeZero())
			g.Expect(cert.KeyUsage & x509.KeyUsageDigitalSignature).NotTo(BeZero())

			// Verify extended key usage
			g.Expect(cert.ExtKeyUsage).To(ContainElement(x509.ExtKeyUsageServerAuth))

			// Verify the private key can be parsed
			_, err = certs.PemToPrivateKey(keyPEM)
			g.Expect(err).NotTo(HaveOccurred())
		})
	}
}
