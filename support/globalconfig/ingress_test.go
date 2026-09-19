package globalconfig

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

func managedDNSHCP(prefix string) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			DNS:      hyperv1.DNSSpec{BaseDomain: "example.com"},
			Platform: hyperv1.PlatformSpec{AWS: &hyperv1.AWSPlatformSpec{ManagedDNS: &hyperv1.AWSManagedDNSSpec{IngressDomainPrefix: prefix}}},
		},
	}
}

func TestManagedDNSIngressZoneDomain(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		expected string
	}{
		{
			// The API server defaults ingressDomainPrefix to "in" when unset.
			name:     "When the prefix is the default, it should root the zone at in.<base>",
			prefix:   "in",
			expected: "in.test-hcp.example.com",
		},
		{
			name:     "When a custom prefix is set, it should root the zone at <prefix>.<base>",
			prefix:   "apps",
			expected: "apps.test-hcp.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp := managedDNSHCP(tt.prefix)
			hcp.Name = "test-hcp"

			g.Expect(ManagedDNSIngressZoneDomain(hcp)).To(Equal(tt.expected))
		})
	}
}

func TestIngressDomain(t *testing.T) {
	t.Run("Without managed DNS it derives the apps domain from the base domain", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{DNS: hyperv1.DNSSpec{BaseDomain: "example.com"}}}
		hcp.Name = "test-hcp"

		g.Expect(IngressDomain(hcp)).To(Equal("apps.test-hcp.example.com"))
	})

	t.Run("With managed DNS the apps domain lives beneath the ingress zone", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := managedDNSHCP("in")
		hcp.Name = "test-hcp"

		// Must be a subdomain of ManagedDNSIngressZoneDomain so Route53 accepts the
		// wildcard record the ingress operator writes into the managed zone.
		g.Expect(IngressDomain(hcp)).To(Equal("apps.in.test-hcp.example.com"))
		g.Expect(IngressDomain(hcp)).To(HaveSuffix("." + ManagedDNSIngressZoneDomain(hcp)))
	})

	t.Run("An explicit apps domain override wins over the managed DNS default", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := managedDNSHCP("in")
		hcp.Name = "test-hcp"
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
			Ingress: &configv1.IngressSpec{AppsDomain: "apps.custom.example.com"},
		}

		g.Expect(IngressDomain(hcp)).To(Equal("apps.custom.example.com"))
	})
}
