package hostedcontrolplane

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestGetZoneIDFromStatus(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		zoneType hyperv1.AWSDNSZoneType
		expected string
	}{
		{
			name: "When zone exists in status it should return the ID",
			hcp: &hyperv1.HostedControlPlane{
				Status: hyperv1.HostedControlPlaneStatus{
					Platform: &hyperv1.PlatformStatus{
						AWS: &hyperv1.AWSPlatformStatus{
							DNSZones: []hyperv1.AWSDNSZoneStatus{
								{ZoneID: "ZPUB", ZoneType: hyperv1.PublicIngressZone, Name: "in.test.example.com"},
							},
						},
					},
				},
			},
			zoneType: hyperv1.PublicIngressZone,
			expected: "ZPUB",
		},
		{
			name: "When zone type is not in status it should return empty",
			hcp: &hyperv1.HostedControlPlane{
				Status: hyperv1.HostedControlPlaneStatus{
					Platform: &hyperv1.PlatformStatus{
						AWS: &hyperv1.AWSPlatformStatus{
							DNSZones: []hyperv1.AWSDNSZoneStatus{
								{ZoneID: "ZPUB", ZoneType: hyperv1.PublicIngressZone, Name: "in.test.example.com"},
							},
						},
					},
				},
			},
			zoneType: hyperv1.PrivateIngressZone,
			expected: "",
		},
		{
			name:     "When status platform is nil it should return empty",
			hcp:      &hyperv1.HostedControlPlane{},
			zoneType: hyperv1.PublicIngressZone,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(getZoneIDFromStatus(tt.hcp, tt.zoneType)).To(Equal(tt.expected))
		})
	}
}

func TestSetZoneInStatus(t *testing.T) {
	g := NewGomegaWithT(t)

	hcp := &hyperv1.HostedControlPlane{}
	setZoneInStatus(hcp, hyperv1.PublicIngressZone, "ZPUB", "in.test.example.com", []string{"ns1.example.com", "ns2.example.com"})
	g.Expect(hcp.Status.Platform.AWS.DNSZones).To(HaveLen(1))
	g.Expect(hcp.Status.Platform.AWS.DNSZones[0].ZoneID).To(Equal("ZPUB"))
	g.Expect(hcp.Status.Platform.AWS.DNSZones[0].NameServers).To(Equal([]string{"ns1.example.com", "ns2.example.com"}))

	setZoneInStatus(hcp, hyperv1.PrivateIngressZone, "ZPRIV", "in.test.example.com", nil)
	g.Expect(hcp.Status.Platform.AWS.DNSZones).To(HaveLen(2))

	setZoneInStatus(hcp, hyperv1.PublicIngressZone, "ZPUB2", "in.test.example.com", []string{"ns3.example.com"})
	g.Expect(hcp.Status.Platform.AWS.DNSZones).To(HaveLen(2))
	g.Expect(getZoneIDFromStatus(hcp, hyperv1.PublicIngressZone)).To(Equal("ZPUB2"))
	g.Expect(hcp.Status.Platform.AWS.DNSZones[0].NameServers).To(Equal([]string{"ns3.example.com"}))
}
