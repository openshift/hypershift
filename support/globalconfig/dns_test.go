package globalconfig

import (
	"fmt"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestReconcileDNSConfig(t *testing.T) {
	fakeHCPName := "cluster"
	fakeBaseDomain := "example.com"
	fakePublicZoneID := "publiczone1"
	fakePrivateZoneID := "privatezone1"

	testsCases := []struct {
		name              string
		inputDNSConfig    *configv1.DNS
		inputHCP          *hyperv1.HostedControlPlane
		expectedDNSConfig *configv1.DNS
	}{
		{
			name:           "when DNS config is empty then default BaseDomain selected",
			inputDNSConfig: &configv1.DNS{},
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: v1.ObjectMeta{
					Name: fakeHCPName,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: fakeBaseDomain,
					},
				},
			},
			expectedDNSConfig: &configv1.DNS{
				Spec: configv1.DNSSpec{
					BaseDomain: fmt.Sprintf("%s.%s", fakeHCPName, fakeBaseDomain),
				},
			},
		},
		{
			name: "when DNS config provided with BaseDomain specified then specified BaseDomain is used",
			inputDNSConfig: &configv1.DNS{
				Spec: configv1.DNSSpec{
					BaseDomain: fakeBaseDomain,
				},
			},
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: v1.ObjectMeta{
					Name: fakeHCPName,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: fakeBaseDomain,
					},
				},
			},
			expectedDNSConfig: &configv1.DNS{
				Spec: configv1.DNSSpec{
					BaseDomain: fakeBaseDomain,
				},
			},
		},
		{
			name:           "when HCP specifies public and private zone then those values propagate to the DNS config",
			inputDNSConfig: &configv1.DNS{},
			inputHCP: &hyperv1.HostedControlPlane{
				ObjectMeta: v1.ObjectMeta{
					Name: fakeHCPName,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain:    fakeBaseDomain,
						PublicZoneID:  fakePublicZoneID,
						PrivateZoneID: fakePrivateZoneID,
					},
				},
			},
			expectedDNSConfig: &configv1.DNS{
				Spec: configv1.DNSSpec{
					BaseDomain: fmt.Sprintf("%s.%s", fakeHCPName, fakeBaseDomain),
					PrivateZone: &configv1.DNSZone{
						ID: fakePrivateZoneID,
					},
					PublicZone: &configv1.DNSZone{
						ID: fakePublicZoneID,
					},
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
