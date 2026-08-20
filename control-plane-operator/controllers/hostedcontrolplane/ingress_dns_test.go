package hostedcontrolplane

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/awsapi"
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

func TestVerifyOrCreateZone(t *testing.T) {
	errCreateNotExpected := func() (string, error) { return "", errors.New("createFn should not be called") }

	tests := []struct {
		name         string
		zoneID       string
		setupMock    func(*awsapi.MockROUTE53API)
		createFn     func() (string, error)
		expectedZone string
		expectError  bool
	}{
		{
			name:         "When zoneID is empty it creates a new zone",
			zoneID:       "",
			setupMock:    func(m *awsapi.MockROUTE53API) {},
			createFn:     func() (string, error) { return "Z-NEW", nil },
			expectedZone: "Z-NEW",
		},
		{
			name:   "When zoneID exists and is valid it is kept without creating",
			zoneID: "Z-EXISTING",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.GetHostedZoneOutput{}, nil)
			},
			createFn:     errCreateNotExpected,
			expectedZone: "Z-EXISTING",
		},
		{
			name:   "When zoneID was deleted externally it clears and recreates",
			zoneID: "Z-GONE",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, &route53types.NoSuchHostedZone{})
			},
			createFn:     func() (string, error) { return "Z-RECREATED", nil },
			expectedZone: "Z-RECREATED",
		},
		{
			name:   "When GetHostedZone fails with a non-NotFound error it returns the error and keeps the zoneID",
			zoneID: "Z-ERR",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("throttled"))
			},
			createFn:     errCreateNotExpected,
			expectError:  true,
			expectedZone: "Z-ERR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			zoneID := tt.zoneID
			err := verifyOrCreateZone(context.Background(), mockR53, &zoneID, "test", tt.createFn, logr.Discard())
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			g.Expect(zoneID).To(Equal(tt.expectedZone))
		})
	}
}
