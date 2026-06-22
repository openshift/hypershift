package globalconfig

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileDNSConfig(t *testing.T) {
	fakeHCPName := "cluster"
	fakeBaseDomain := "example.com"
	fakeBaseDomainPrefix := "fake-prefix"
	fakePublicZoneID := "publiczone1"
	fakePrivateZoneID := "privatezone1"
	testsCases := []struct {
		name              string
		inputHCP          *hyperv1.HostedControlPlane
		inputDNSConfig    *configv1.DNS
		expectedDNSConfig *configv1.DNS
	}{
		{
			name:           "when DNS parameters specified on the HostedControlPlane then they are copied to the DNS object",
			inputDNSConfig: DNSConfig(),
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: v1.ObjectMeta{
					Name: fakeHCPName,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain:    fakeBaseDomain,
						PrivateZoneID: fakePrivateZoneID,
						PublicZoneID:  fakePublicZoneID,
					},
				},
			},
			expectedDNSConfig: &configv1.DNS{
				ObjectMeta: v1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.DNSSpec{
					BaseDomain: fmt.Sprintf("%s.%s", fakeHCPName, fakeBaseDomain),
					PublicZone: &configv1.DNSZone{
						ID: fakePublicZoneID,
					},
					PrivateZone: &configv1.DNSZone{
						ID: fakePrivateZoneID,
					},
				},
			},
		},
		{
			name:           "when IBM Cloud platform is used then the base domain is set to the value on the HostedControlPlane",
			inputDNSConfig: DNSConfig(),
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: v1.ObjectMeta{
					Name: fakeHCPName,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: fakeBaseDomain,
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
				},
			},
			expectedDNSConfig: &configv1.DNS{
				ObjectMeta: v1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.DNSSpec{
					BaseDomain: fakeBaseDomain,
				},
			},
		},
		{
			name:           "when DNS BaseDomainPrefix is specified on the HostedControlPlane then, it will be used on the DNS object instead of the hcp Name",
			inputDNSConfig: DNSConfig(),
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: v1.ObjectMeta{
					Name: fakeHCPName,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain:       fakeBaseDomain,
						BaseDomainPrefix: &fakeBaseDomainPrefix,
					},
				},
			},
			expectedDNSConfig: &configv1.DNS{
				ObjectMeta: v1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.DNSSpec{
					BaseDomain: fmt.Sprintf("%s.%s", fakeBaseDomainPrefix, fakeBaseDomain),
				},
			},
		},
		{
			name:           "when DNS BaseDomainPrefix is set to empty string on the HostedControlPlane then, DNS config BaseDomain will not prepended with any prefix",
			inputDNSConfig: DNSConfig(),
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: v1.ObjectMeta{
					Name: fakeHCPName,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain:       fakeBaseDomain,
						BaseDomainPrefix: new(string),
					},
				},
			},
			expectedDNSConfig: &configv1.DNS{
				ObjectMeta: v1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.DNSSpec{
					BaseDomain: fakeBaseDomain,
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ReconcileDNSConfig(tc.inputDNSConfig, tc.inputHCP)
			g.Expect(tc.expectedDNSConfig).To(BeEquivalentTo(tc.inputDNSConfig))
		})
	}
}
