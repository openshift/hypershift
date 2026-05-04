package awsprivatelink

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIngressDNSName(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected string
	}{
		{
			name: "When BaseDomainPrefix is nil it should use HCP name as prefix",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cluster"},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
				},
			},
			expected: "my-cluster.example.com",
		},
		{
			name: "When BaseDomainPrefix is set it should use the prefix",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cluster"},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain:       "example.com",
						BaseDomainPrefix: aws.String("custom"),
					},
				},
			},
			expected: "custom.example.com",
		},
		{
			name: "When BaseDomainPrefix is empty string it should use HCP name as prefix",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cluster"},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS: hyperv1.DNSSpec{
						BaseDomain:       "example.com",
						BaseDomainPrefix: aws.String(""),
					},
				},
			},
			expected: "my-cluster.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(ingressDNSName(tt.hcp)).To(Equal(tt.expected))
		})
	}
}

func TestCallerReference(t *testing.T) {
	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "clusters",
		},
	}

	t.Run("When called with same inputs it should return the same reference", func(t *testing.T) {
		ref1 := callerReference(hcp, "zone-name")
		ref2 := callerReference(hcp, "zone-name")
		g.Expect(ref1).To(Equal(ref2))
	})

	t.Run("When called with different zone names it should return different references", func(t *testing.T) {
		ref1 := callerReference(hcp, "zone-a")
		ref2 := callerReference(hcp, "zone-b")
		g.Expect(ref1).NotTo(Equal(ref2))
	})
}

func TestCreateOrGetPublicHostedZone(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockROUTE53API)
		expectZoneID  string
		expectNS      []string
		expectError   bool
		errorContains string
	}{
		{
			name: "When zone does not exist it should create it and return zone ID and NS records",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.CreateHostedZoneOutput{
						HostedZone: &route53types.HostedZone{
							Id: aws.String("/hostedzone/Z123"),
						},
						DelegationSet: &route53types.DelegationSet{
							NameServers: []string{"ns-1.awsdns.com", "ns-2.awsdns.com"},
						},
					}, nil,
				)
			},
			expectZoneID: "Z123",
			expectNS:     []string{"ns-1.awsdns.com", "ns-2.awsdns.com"},
		},
		{
			name: "When zone already exists with matching CallerReference it should return zone ID and NS records",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &route53types.HostedZoneAlreadyExists{Message: aws.String("exists")},
				)
				m.EXPECT().ListHostedZonesByName(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesByNameOutput{
						HostedZones: []route53types.HostedZone{
							{
								Id:   aws.String("/hostedzone/Z456"),
								Name: aws.String("test.example.com."),
								Config: &route53types.HostedZoneConfig{
									PrivateZone: false,
								},
							},
						},
					}, nil,
				)
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.GetHostedZoneOutput{
						HostedZone: &route53types.HostedZone{
							Id:              aws.String("/hostedzone/Z456"),
							CallerReference: aws.String("callerref"),
						},
						DelegationSet: &route53types.DelegationSet{
							NameServers: []string{"ns-3.awsdns.com"},
						},
					}, nil,
				)
			},
			expectZoneID: "Z456",
			expectNS:     []string{"ns-3.awsdns.com"},
		},
		{
			name: "When zone already exists with mismatched CallerReference it should refuse to adopt",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &route53types.HostedZoneAlreadyExists{Message: aws.String("exists")},
				)
				m.EXPECT().ListHostedZonesByName(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesByNameOutput{
						HostedZones: []route53types.HostedZone{
							{
								Id:   aws.String("/hostedzone/ZOTHER"),
								Name: aws.String("test.example.com."),
								Config: &route53types.HostedZoneConfig{
									PrivateZone: false,
								},
							},
						},
					}, nil,
				)
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.GetHostedZoneOutput{
						HostedZone: &route53types.HostedZone{
							Id:              aws.String("/hostedzone/ZOTHER"),
							CallerReference: aws.String("someone-elses-ref"),
						},
					}, nil,
				)
			},
			expectError:   true,
			errorContains: "unexpected CallerReference",
		},
		{
			name: "When create fails with non-exists error it should return the error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &route53types.InvalidInput{Message: aws.String("bad input")},
				)
			},
			expectError:   true,
			errorContains: "failed to create public hosted zone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			zoneID, ns, err := createOrGetPublicHostedZone(context.Background(), mockR53, "test.example.com", "callerref")

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(zoneID).To(Equal(tt.expectZoneID))
				g.Expect(ns).To(Equal(tt.expectNS))
			}
		})
	}
}

func TestCreateOrGetPrivateHostedZone(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockROUTE53API)
		expectZoneID  string
		expectError   bool
		errorContains string
	}{
		{
			name: "When zone does not exist it should create it and return zone ID",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.CreateHostedZoneOutput{
						HostedZone: &route53types.HostedZone{
							Id: aws.String("/hostedzone/ZPRIV1"),
						},
					}, nil,
				)
			},
			expectZoneID: "ZPRIV1",
		},
		{
			name: "When zone already exists with correct VPC and matching CallerReference it should return zone ID",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &route53types.HostedZoneAlreadyExists{Message: aws.String("exists")},
				)
				m.EXPECT().ListHostedZonesByName(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesByNameOutput{
						HostedZones: []route53types.HostedZone{
							{
								Id:   aws.String("/hostedzone/ZPRIV2"),
								Name: aws.String("test.example.com."),
								Config: &route53types.HostedZoneConfig{
									PrivateZone: true,
								},
							},
						},
					}, nil,
				)
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.GetHostedZoneOutput{
						HostedZone: &route53types.HostedZone{
							Id:              aws.String("/hostedzone/ZPRIV2"),
							CallerReference: aws.String("callerref"),
						},
						VPCs: []route53types.VPC{
							{VPCId: aws.String("vpc-123"), VPCRegion: route53types.VPCRegionUsEast1},
						},
					}, nil,
				)
			},
			expectZoneID: "ZPRIV2",
		},
		{
			name: "When zone already exists without VPC association it should associate the VPC",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &route53types.HostedZoneAlreadyExists{Message: aws.String("exists")},
				)
				m.EXPECT().ListHostedZonesByName(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesByNameOutput{
						HostedZones: []route53types.HostedZone{
							{
								Id:   aws.String("/hostedzone/ZPRIV3"),
								Name: aws.String("test.example.com."),
								Config: &route53types.HostedZoneConfig{
									PrivateZone: true,
								},
							},
						},
					}, nil,
				)
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.GetHostedZoneOutput{
						HostedZone: &route53types.HostedZone{
							Id:              aws.String("/hostedzone/ZPRIV3"),
							CallerReference: aws.String("callerref"),
						},
						VPCs: []route53types.VPC{
							{VPCId: aws.String("vpc-other"), VPCRegion: route53types.VPCRegionUsEast1},
						},
					}, nil,
				)
				m.EXPECT().AssociateVPCWithHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.AssociateVPCWithHostedZoneOutput{}, nil,
				)
			},
			expectZoneID: "ZPRIV3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			zoneID, err := createOrGetPrivateHostedZone(context.Background(), mockR53, "test.example.com", "callerref", "vpc-123", "us-east-1")

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(zoneID).To(Equal(tt.expectZoneID))
			}
		})
	}
}

func TestCreateACMEChallengeRecord(t *testing.T) {
	t.Run("When called it should create a CNAME from _acme-challenge.apps.ingress to _acme-challenge.baseDomain", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)

		mockR53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, input *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
				g.Expect(aws.ToString(input.HostedZoneId)).To(Equal("ZPUB"))
				changes := input.ChangeBatch.Changes
				g.Expect(changes).To(HaveLen(1))
				g.Expect(aws.ToString(changes[0].ResourceRecordSet.Name)).To(Equal("_acme-challenge.apps.cluster.example.com"))
				g.Expect(changes[0].ResourceRecordSet.Type).To(Equal(route53types.RRTypeCname))
				g.Expect(changes[0].ResourceRecordSet.ResourceRecords).To(HaveLen(1))
				g.Expect(aws.ToString(changes[0].ResourceRecordSet.ResourceRecords[0].Value)).To(Equal("_acme-challenge.example.com"))
				return &route53.ChangeResourceRecordSetsOutput{}, nil
			},
		)

		err := createACMEChallengeRecord(context.Background(), mockR53, "ZPUB", "cluster.example.com", "example.com")
		g.Expect(err).NotTo(HaveOccurred())
	})
}

func TestReconcileIngressDNS(t *testing.T) {
	baseHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "clusters",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				DNS: hyperv1.DNSSpec{
					BaseDomain: "example.com",
				},
				Platform: hyperv1.PlatformSpec{
					AWS: &hyperv1.AWSPlatformSpec{
						Region: "us-east-1",
						CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
							VPC: "vpc-123",
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name              string
		hcp               *hyperv1.HostedControlPlane
		existingPublicID  string
		existingPrivateID string
		setupMock         func(*awsapi.MockROUTE53API)
		expectError       bool
		errorContains     string
		validateResult    func(*GomegaWithT, *ingressDNSResult)
	}{
		{
			name: "When no zones exist it should create both public and private zones",
			hcp:  baseHCP(),
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, input *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
						if input.VPC == nil {
							return &route53.CreateHostedZoneOutput{
								HostedZone:    &route53types.HostedZone{Id: aws.String("/hostedzone/ZPUB")},
								DelegationSet: &route53types.DelegationSet{NameServers: []string{"ns-1.awsdns.com"}},
							}, nil
						}
						return &route53.CreateHostedZoneOutput{
							HostedZone: &route53types.HostedZone{Id: aws.String("/hostedzone/ZPRIV")},
						}, nil
					},
				).Times(2)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ChangeResourceRecordSetsOutput{}, nil,
				)
			},
			validateResult: func(g *GomegaWithT, result *ingressDNSResult) {
				g.Expect(result.PublicZoneID).To(Equal("ZPUB"))
				g.Expect(result.PrivateZoneID).To(Equal("ZPRIV"))
				g.Expect(result.NSRecords).To(Equal([]string{"ns-1.awsdns.com"}))
				g.Expect(result.IngressDNS).To(Equal("my-cluster.example.com"))
			},
		},
		{
			name:              "When both zones already exist it should validate and fetch NS records",
			hcp:               baseHCP(),
			existingPublicID:  "ZPUB-EXISTING",
			existingPrivateID: "ZPRIV-EXISTING",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, input *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
						if aws.ToString(input.Id) == "ZPUB-EXISTING" {
							return &route53.GetHostedZoneOutput{
								HostedZone:    &route53types.HostedZone{Id: aws.String("ZPUB-EXISTING")},
								DelegationSet: &route53types.DelegationSet{NameServers: []string{"ns-existing.awsdns.com"}},
							}, nil
						}
						return &route53.GetHostedZoneOutput{
							HostedZone: &route53types.HostedZone{Id: aws.String("ZPRIV-EXISTING")},
							VPCs: []route53types.VPC{
								{VPCId: aws.String("vpc-123"), VPCRegion: route53types.VPCRegionUsEast1},
							},
						}, nil
					},
				).Times(2)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ChangeResourceRecordSetsOutput{}, nil,
				)
			},
			validateResult: func(g *GomegaWithT, result *ingressDNSResult) {
				g.Expect(result.PublicZoneID).To(Equal("ZPUB-EXISTING"))
				g.Expect(result.PrivateZoneID).To(Equal("ZPRIV-EXISTING"))
				g.Expect(result.NSRecords).To(Equal([]string{"ns-existing.awsdns.com"}))
			},
		},
		{
			name:              "When existing private zone is deleted it should recreate it",
			hcp:               baseHCP(),
			existingPublicID:  "ZPUB-EXISTING",
			existingPrivateID: "ZPRIV-GONE",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, input *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
						if aws.ToString(input.Id) == "ZPUB-EXISTING" {
							return &route53.GetHostedZoneOutput{
								HostedZone:    &route53types.HostedZone{Id: aws.String("ZPUB-EXISTING")},
								DelegationSet: &route53types.DelegationSet{NameServers: []string{"ns-existing.awsdns.com"}},
							}, nil
						}
						return nil, &route53types.NoSuchHostedZone{Message: aws.String("not found")}
					},
				).Times(2)
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.CreateHostedZoneOutput{
						HostedZone: &route53types.HostedZone{Id: aws.String("/hostedzone/ZPRIV-NEW")},
					}, nil,
				)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ChangeResourceRecordSetsOutput{}, nil,
				)
			},
			validateResult: func(g *GomegaWithT, result *ingressDNSResult) {
				g.Expect(result.PublicZoneID).To(Equal("ZPUB-EXISTING"))
				g.Expect(result.PrivateZoneID).To(Equal("ZPRIV-NEW"))
				g.Expect(result.NSRecords).To(Equal([]string{"ns-existing.awsdns.com"}))
			},
		},
		{
			name: "When AWS platform spec is nil it should return an error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: "clusters"},
				Spec: hyperv1.HostedControlPlaneSpec{
					DNS:      hyperv1.DNSSpec{BaseDomain: "example.com"},
					Platform: hyperv1.PlatformSpec{},
				},
			},
			setupMock:     func(m *awsapi.MockROUTE53API) {},
			expectError:   true,
			errorContains: "AWS platform spec is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			result, err := reconcileIngressDNS(context.Background(), mockR53, tt.hcp, tt.existingPublicID, tt.existingPrivateID)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				if tt.validateResult != nil {
					tt.validateResult(g, result)
				}
			}
		})
	}
}

func TestCleanupIngressDNS(t *testing.T) {
	tests := []struct {
		name          string
		publicZoneID  string
		privateZoneID string
		setupMock     func(*awsapi.MockROUTE53API)
		expectError   bool
		errorContains string
	}{
		{
			name:          "When both zone IDs are set it should delete both zones",
			publicZoneID:  "ZPUB",
			privateZoneID: "ZPRIV",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{Type: route53types.RRTypeSoa, Name: aws.String("soa")},
							{Type: route53types.RRTypeNs, Name: aws.String("ns")},
						},
					}, nil,
				).Times(2)
				m.EXPECT().DeleteHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.DeleteHostedZoneOutput{}, nil,
				).Times(2)
			},
		},
		{
			name:          "When zones have custom records it should delete records before deleting the zone",
			publicZoneID:  "ZPUB",
			privateZoneID: "",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{Type: route53types.RRTypeSoa, Name: aws.String("soa")},
							{Type: route53types.RRTypeCname, Name: aws.String("_acme-challenge.apps.test.example.com")},
						},
					}, nil,
				)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ChangeResourceRecordSetsOutput{}, nil,
				)
				m.EXPECT().DeleteHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.DeleteHostedZoneOutput{}, nil,
				)
			},
		},
		{
			name:          "When zone is already deleted it should succeed without error",
			publicZoneID:  "ZGONE",
			privateZoneID: "",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &route53types.NoSuchHostedZone{Message: aws.String("not found")},
				)
			},
		},
		{
			name:          "When both zone IDs are empty it should be a no-op",
			publicZoneID:  "",
			privateZoneID: "",
			setupMock:     func(m *awsapi.MockROUTE53API) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			err := cleanupIngressDNS(context.Background(), mockR53, tt.publicZoneID, tt.privateZoneID)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestLookupIngressZoneID(t *testing.T) {
	tests := []struct {
		name          string
		zoneName      string
		privateZone   bool
		setupMock     func(*awsapi.MockROUTE53API)
		expectZoneID  string
		expectError   bool
		errorContains string
	}{
		{
			name:        "When matching public zone exists it should return its ID",
			zoneName:    "test.example.com",
			privateZone: false,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZonesByName(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesByNameOutput{
						HostedZones: []route53types.HostedZone{
							{
								Id:     aws.String("/hostedzone/ZFOUND"),
								Name:   aws.String("test.example.com."),
								Config: &route53types.HostedZoneConfig{PrivateZone: false},
							},
						},
					}, nil,
				)
			},
			expectZoneID: "ZFOUND",
		},
		{
			name:        "When no matching zone exists it should return an error",
			zoneName:    "missing.example.com",
			privateZone: false,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZonesByName(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesByNameOutput{
						HostedZones: []route53types.HostedZone{},
					}, nil,
				)
			},
			expectError:   true,
			errorContains: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			zoneID, err := lookupIngressZoneID(context.Background(), mockR53, tt.zoneName, tt.privateZone)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(zoneID).To(Equal(tt.expectZoneID))
			}
		})
	}
}
