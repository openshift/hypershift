package aws

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"go.uber.org/mock/gomock"
)

// cancelledCtx returns a pre-canceled context. Used to prevent
// retryRoute53WithBackoff from sleeping between retries in error test cases.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

const (
	testZoneName   = "internal.example.com"
	testBaseDomain = "example.com"
	testCluster    = "mycluster"
	testVPCID      = "vpc-12345"
	testInitialVPC = "vpc-initial"
	// validSOAValue is a 7-field SOA record value as required by setSOAMinimum.
	validSOAValue = "ns-1.awsdns-1.org. hostmaster.example.com. 1 7200 900 1209600 86400"
)

// soaRecordFor returns a mock ListResourceRecordSets response containing a
// single SOA record for the given zone name.
func soaRecordFor(name string) *route53.ListResourceRecordSetsOutput {
	return &route53.ListResourceRecordSetsOutput{
		ResourceRecordSets: []route53types.ResourceRecordSet{
			{
				Name: aws.String(name + "."),
				Type: route53types.RRTypeSoa,
				ResourceRecords: []route53types.ResourceRecord{
					{Value: aws.String(validSOAValue)},
				},
			},
		},
	}
}

// publicZonePage returns a single-page ListHostedZones response with one public zone.
func publicZonePage(id, name string) *route53.ListHostedZonesOutput {
	return &route53.ListHostedZonesOutput{
		HostedZones: []route53types.HostedZone{
			{
				Id:     aws.String("/hostedzone/" + id),
				Name:   aws.String(name + "."),
				Config: &route53types.HostedZoneConfig{PrivateZone: false},
			},
		},
	}
}

// privateZonePage returns a single-page ListHostedZones response with one private zone.
func privateZonePage(id, name string) *route53.ListHostedZonesOutput {
	return &route53.ListHostedZonesOutput{
		HostedZones: []route53types.HostedZone{
			{
				Id:     aws.String("/hostedzone/" + id),
				Name:   aws.String(name + "."),
				Config: &route53types.HostedZoneConfig{PrivateZone: true},
			},
		},
	}
}

// emptyZonePage returns a single-page ListHostedZones response with no zones.
func emptyZonePage() *route53.ListHostedZonesOutput {
	return &route53.ListHostedZonesOutput{HostedZones: []route53types.HostedZone{}}
}

func hostedZonesForVPC(ids ...string) *route53.ListHostedZonesByVPCOutput {
	output := &route53.ListHostedZonesByVPCOutput{}
	for _, id := range ids {
		output.HostedZoneSummaries = append(output.HostedZoneSummaries, route53types.HostedZoneSummary{HostedZoneId: aws.String("/hostedzone/" + id)})
	}
	return output
}

func TestLookupPublicZone(t *testing.T) {
	tests := []struct {
		name        string
		baseDomain  string
		redact      bool
		setupMock   func(*awsapi.MockROUTE53API)
		expectID    string
		expectError bool
		useCtx      func() context.Context
	}{
		{
			name:       "When the public zone exists it should return its ID",
			baseDomain: testBaseDomain,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(publicZonePage("PUBZONE", testBaseDomain), nil)
			},
			expectID: "PUBZONE",
		},
		{
			name:       "When the zone API call fails, it should return an error",
			baseDomain: testBaseDomain,
			useCtx:     cancelledCtx,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("api error"))
			},
			expectError: true,
		},
		{
			name:       "When redact is true and zone lookup fails, it should return error without logging the domain",
			baseDomain: "secret.example.com",
			redact:     true,
			useCtx:     cancelledCtx,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("api error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			ctx := context.Background()
			if tt.useCtx != nil {
				ctx = tt.useCtx()
			}

			var logBuf strings.Builder
			logger := funcr.New(func(prefix, args string) {
				logBuf.WriteString(prefix + args + "\n")
			}, funcr.Options{})

			o := &CreateInfraOptions{BaseDomain: tt.baseDomain, RedactBaseDomain: tt.redact}
			id, err := o.LookupPublicZone(ctx, logger, mockR53)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(id).To(Equal(tt.expectID))
			}

			if tt.redact {
				g.Expect(err.Error()).NotTo(ContainSubstring(tt.baseDomain))
				g.Expect(logBuf.String()).NotTo(ContainSubstring(tt.baseDomain))
			}
		})
	}
}

func TestLookupZone(t *testing.T) {
	t.Run("When a pagination token repeats non-consecutively, it should return a redacted duplicate-token error", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		firstToken := "opaque-hosted-zone-token-a"
		secondToken := "opaque-hosted-zone-token-b"

		gomock.InOrder(
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					g.Expect(input.Marker).To(BeNil())
					return &route53.ListHostedZonesOutput{IsTruncated: true, NextMarker: aws.String(firstToken)}, nil
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					g.Expect(aws.ToString(input.Marker)).To(Equal(firstToken))
					return &route53.ListHostedZonesOutput{IsTruncated: true, NextMarker: aws.String(secondToken)}, nil
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					g.Expect(aws.ToString(input.Marker)).To(Equal(secondToken))
					return &route53.ListHostedZonesOutput{IsTruncated: true, NextMarker: aws.String(firstToken)}, nil
				}),
		)

		_, err := LookupZone(cancelledCtx(), mockR53, testZoneName, false)
		g.Expect(err).To(MatchError("failed to list hosted zones: duplicate pagination token"))
		g.Expect(err.Error()).NotTo(ContainSubstring(firstToken))
		g.Expect(err.Error()).NotTo(ContainSubstring(secondToken))
	})
}

func TestCreatePrivateZone(t *testing.T) {
	tests := []struct {
		name              string
		zoneName          string
		vpcID             string
		authorizeAssoc    bool
		initialVPC        string
		setupMock         func(*awsapi.MockROUTE53API)
		setupVPCOwnerMock func(*awsapi.MockROUTE53API)
		expectID          string
		expectError       bool
		errorContains     string
		errorNotContains  []string
		useCtx            func() context.Context
	}{
		{
			name:     "When the private zone already exists it should update the SOA minimum and return its ID",
			zoneName: testZoneName,
			vpcID:    testVPCID,
			setupMock: func(m *awsapi.MockROUTE53API) {
				// lookupZones finds the existing zone
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(privateZonePage("EXISTZONE", testZoneName), nil)
				m.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(hostedZonesForVPC("EXISTZONE"), nil)
				// setSOAMinimum: findRecord + update
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(soaRecordFor(testZoneName), nil)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ChangeResourceRecordSetsOutput{}, nil)
			},
			setupVPCOwnerMock: func(_ *awsapi.MockROUTE53API) {},
			expectID:          "EXISTZONE",
		},
		{
			name:     "When a same-account private zone belongs to another VPC, it should return an error without updating the SOA record",
			zoneName: testZoneName,
			vpcID:    testVPCID,
			useCtx:   cancelledCtx,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(privateZonePage("UNRELATEDZONE", testZoneName), nil)
				m.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(hostedZonesForVPC("OTHERZONE"), nil)
			},
			setupVPCOwnerMock: func(_ *awsapi.MockROUTE53API) {},
			expectError:       true,
			errorContains:     "no matching hosted zone association",
			errorNotContains:  []string{testZoneName, testVPCID, "UNRELATEDZONE"},
		},
		{
			name:     "When the private zone does not exist it should create it and return the new ID",
			zoneName: testZoneName,
			vpcID:    testVPCID,
			setupMock: func(m *awsapi.MockROUTE53API) {
				// lookupZones finds no zone
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(emptyZonePage(), nil)
				// CreateHostedZone
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.CreateHostedZoneOutput{
						HostedZone: &route53types.HostedZone{
							Id:   aws.String("/hostedzone/NEWZONE"),
							Name: aws.String(testZoneName + "."),
						},
					}, nil)
				m.EXPECT().ChangeTagsForResource(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ChangeTagsForResourceOutput{}, nil)
				// setSOAMinimum: findRecord + update
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(soaRecordFor(testZoneName), nil)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ChangeResourceRecordSetsOutput{}, nil)
			},
			setupVPCOwnerMock: func(_ *awsapi.MockROUTE53API) {},
			expectID:          "NEWZONE",
		},
		{
			name:           "When authorizeAssociation is true it should wire cross-account VPC association and return the ID",
			zoneName:       testZoneName,
			vpcID:          testVPCID,
			authorizeAssoc: true,
			initialVPC:     testInitialVPC,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(emptyZonePage(), nil)
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.CreateHostedZoneOutput{
						HostedZone: &route53types.HostedZone{
							Id:   aws.String("/hostedzone/AUTHZONE"),
							Name: aws.String(testZoneName + "."),
						},
					}, nil)
				m.EXPECT().ChangeTagsForResource(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ChangeTagsForResourceOutput{}, nil)
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(soaRecordFor(testZoneName), nil)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ChangeResourceRecordSetsOutput{}, nil)
				m.EXPECT().CreateVPCAssociationAuthorization(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.CreateVPCAssociationAuthorizationOutput{}, nil)
				m.EXPECT().DisassociateVPCFromHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.DisassociateVPCFromHostedZoneOutput{}, nil)
			},
			setupVPCOwnerMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().AssociateVPCWithHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.AssociateVPCWithHostedZoneOutput{}, nil)
			},
			expectID: "AUTHZONE",
		},
		{
			name:     "When CreateHostedZone fails, it should return a wrapped error",
			zoneName: testZoneName,
			vpcID:    testVPCID,
			useCtx:   cancelledCtx,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(emptyZonePage(), nil)
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("create failed"))
			},
			setupVPCOwnerMock: func(_ *awsapi.MockROUTE53API) {},
			expectError:       true,
			errorContains:     "failed to create hosted zone",
		},
		{
			name:     "When setSOAMinimum fails on an existing zone, it should return an error",
			zoneName: testZoneName,
			vpcID:    testVPCID,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(privateZonePage("EXISTZONE", testZoneName), nil)
				m.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(hostedZonesForVPC("EXISTZONE"), nil)
				// setSOAMinimum → findRecord fails
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("records error"))
			},
			setupVPCOwnerMock: func(_ *awsapi.MockROUTE53API) {},
			expectError:       true,
			errorContains:     "records error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			mockVPCOwner := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)
			tt.setupVPCOwnerMock(mockVPCOwner)

			ctx := context.Background()
			if tt.useCtx != nil {
				ctx = tt.useCtx()
			}

			o := &CreateInfraOptions{Region: "us-east-1"}
			id, err := o.CreatePrivateZone(ctx, logr.Discard(), mockR53, tt.zoneName, tt.vpcID, tt.authorizeAssoc, mockVPCOwner, tt.initialVPC)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got: %v", tt.errorContains, err)
				}
				for _, value := range tt.errorNotContains {
					if strings.Contains(err.Error(), value) {
						t.Errorf("expected error not to contain %q, got: %v", value, err)
					}
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
				if id != tt.expectID {
					t.Errorf("expected zone ID %q, got %q", tt.expectID, id)
				}
			}
		})
	}

	t.Run("When a cross-account private zone is associated with the target VPC, it should reuse the zone", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		mockVPCOwner := awsapi.NewMockROUTE53API(ctrl)

		mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privateZonePage("EXISTZONE", testZoneName), nil)
		mockVPCOwner.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
				g.Expect(aws.ToString(input.VPCId)).To(Equal(testVPCID))
				g.Expect(input.VPCRegion).To(Equal(route53types.VPCRegionUsEast1))
				return hostedZonesForVPC("EXISTZONE"), nil
			})
		mockR53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(soaRecordFor(testZoneName), nil)
		mockR53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ChangeResourceRecordSetsOutput{}, nil)

		o := &CreateInfraOptions{Region: "us-east-1"}
		id, err := o.CreatePrivateZone(t.Context(), logr.Discard(), mockR53, testZoneName, testVPCID, true, mockVPCOwner, testInitialVPC)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(id).To(Equal("EXISTZONE"))
	})

	t.Run("When a cross-account private zone is associated only with the bootstrap VPC, it should return an error without updating the SOA record", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		mockVPCOwner := awsapi.NewMockROUTE53API(ctrl)

		mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privateZonePage("UNRELATEDZONE", testZoneName), nil)
		mockVPCOwner.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
				g.Expect(aws.ToString(input.VPCId)).To(Equal(testVPCID))
				g.Expect(aws.ToString(input.VPCId)).NotTo(Equal(testInitialVPC))
				g.Expect(input.VPCRegion).To(Equal(route53types.VPCRegionUsEast1))
				return hostedZonesForVPC(), nil
			})

		o := &CreateInfraOptions{Region: "us-east-1"}
		id, err := o.CreatePrivateZone(cancelledCtx(), logr.Discard(), mockR53, testZoneName, testVPCID, true, mockVPCOwner, testInitialVPC)
		g.Expect(err).To(MatchError(ContainSubstring("no matching hosted zone association")))
		g.Expect(id).To(BeEmpty())
	})

	t.Run("When the first same-name zone is unrelated and a later zone is associated with the target VPC, it should reuse the associated zone", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)

		unrelatedPage := privateZonePage("UNRELATEDZONE", testZoneName)
		unrelatedPage.IsTruncated = true
		unrelatedPage.NextMarker = aws.String("next-zone-page")
		unrelatedAssociationPage := hostedZonesForVPC("OTHERZONE")
		unrelatedAssociationPage.NextToken = aws.String("next-association-page")
		gomock.InOrder(
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					g.Expect(input.Marker).To(BeNil())
					return unrelatedPage, nil
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					g.Expect(aws.ToString(input.Marker)).To(Equal("next-zone-page"))
					return privateZonePage("TARGETZONE", testZoneName), nil
				}),
			mockR53.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
					g.Expect(aws.ToString(input.VPCId)).To(Equal(testVPCID))
					g.Expect(input.NextToken).To(BeNil())
					return unrelatedAssociationPage, nil
				}),
			mockR53.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
					g.Expect(aws.ToString(input.NextToken)).To(Equal("next-association-page"))
					return hostedZonesForVPC("TARGETZONE"), nil
				}),
		)
		mockR53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, input *route53.ListResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
				g.Expect(aws.ToString(input.HostedZoneId)).To(Equal("TARGETZONE"))
				return soaRecordFor(testZoneName), nil
			})
		mockR53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ChangeResourceRecordSetsOutput{}, nil)

		o := &CreateInfraOptions{Region: "us-east-1"}
		id, err := o.CreatePrivateZone(t.Context(), logr.Discard(), mockR53, testZoneName, testVPCID, false, mockR53, "")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(id).To(Equal("TARGETZONE"))
	})

	t.Run("When a cross-account target VPC association becomes visible after a delay, it should reuse the existing zone", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		mockVPCOwner := awsapi.NewMockROUTE53API(ctrl)

		mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(privateZonePage("EXISTZONE", testZoneName), nil)
		gomock.InOrder(
			mockVPCOwner.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
					g.Expect(aws.ToString(input.VPCId)).To(Equal(testVPCID))
					return hostedZonesForVPC(), nil
				}),
			mockVPCOwner.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(hostedZonesForVPC("EXISTZONE"), nil),
		)
		mockR53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(soaRecordFor(testZoneName), nil)
		mockR53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ChangeResourceRecordSetsOutput{}, nil)

		o := &CreateInfraOptions{Region: "us-east-1"}
		id, err := o.CreatePrivateZone(t.Context(), logr.Discard(), mockR53, testZoneName, testVPCID, true, mockVPCOwner, testInitialVPC)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(id).To(Equal("EXISTZONE"))
	})

	t.Run("When distinct zones are created concurrently, it should use unique caller references", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		var callerReferences []string
		var callerReferencesLock sync.Mutex

		mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(2).
			Return(emptyZonePage(), nil)
		mockR53.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(2).
			DoAndReturn(func(_ context.Context, input *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
				callerReferencesLock.Lock()
				callerReferences = append(callerReferences, aws.ToString(input.CallerReference))
				callerReferencesLock.Unlock()
				return &route53.CreateHostedZoneOutput{
					HostedZone: &route53types.HostedZone{Id: aws.String("/hostedzone/CREATEDZONE")},
				}, nil
			})
		mockR53.EXPECT().ChangeTagsForResource(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(2).
			Return(&route53.ChangeTagsForResourceOutput{}, nil)
		mockR53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(2).
			DoAndReturn(func(_ context.Context, input *route53.ListResourceRecordSetsInput, _ ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
				return soaRecordFor(strings.TrimSuffix(aws.ToString(input.StartRecordName), ".")), nil
			})
		mockR53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(2).
			Return(&route53.ChangeResourceRecordSetsOutput{}, nil)

		o := &CreateInfraOptions{Region: "us-east-1"}
		errs := make(chan error, 2)
		for _, zoneName := range []string{"first.internal.example.com", "second.internal.example.com"} {
			go func() {
				_, err := o.CreatePrivateZone(t.Context(), logr.Discard(), mockR53, zoneName, testVPCID, false, mockR53, "")
				errs <- err
			}()
		}
		g.Expect(<-errs).NotTo(HaveOccurred())
		g.Expect(<-errs).NotTo(HaveOccurred())
		g.Expect(callerReferences).To(HaveLen(2))
		g.Expect(callerReferences[0]).NotTo(BeEmpty())
		g.Expect(callerReferences[1]).NotTo(BeEmpty())
		g.Expect(callerReferences[0]).NotTo(Equal(callerReferences[1]))
	})

	t.Run("When hosted zone creation is retried, it should preserve the caller reference", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		var callerReferences []string
		attempt := 0

		mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(emptyZonePage(), nil)
		mockR53.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
			Times(2).
			DoAndReturn(func(_ context.Context, input *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
				callerReferences = append(callerReferences, aws.ToString(input.CallerReference))
				attempt++
				if attempt == 1 {
					return nil, errors.New("transient error")
				}
				return &route53.CreateHostedZoneOutput{
					HostedZone: &route53types.HostedZone{Id: aws.String("/hostedzone/RETRIEDZONE")},
				}, nil
			})
		mockR53.EXPECT().ChangeTagsForResource(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ChangeTagsForResourceOutput{}, nil)
		mockR53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(soaRecordFor(testZoneName), nil)
		mockR53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ChangeResourceRecordSetsOutput{}, nil)

		o := &CreateInfraOptions{Region: "us-east-1"}
		id, err := o.CreatePrivateZone(t.Context(), logr.Discard(), mockR53, testZoneName, testVPCID, false, mockR53, "")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(id).To(Equal("RETRIEDZONE"))
		g.Expect(callerReferences).To(HaveLen(2))
		g.Expect(callerReferences[0]).To(Equal(callerReferences[1]))
	})

	t.Run("When duplicate-response recovery sees a non-consecutive repeated pagination token, it should return a redacted duplicate-token error", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		firstToken := "opaque-recovery-token-a"
		secondToken := "opaque-recovery-token-b"
		var callerReference string

		gomock.InOrder(
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(emptyZonePage(), nil),
			mockR53.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
					callerReference = aws.ToString(input.CallerReference)
					return nil, &route53types.HostedZoneAlreadyExists{Message: aws.String("already created")}
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					g.Expect(input.Marker).To(BeNil())
					return &route53.ListHostedZonesOutput{IsTruncated: true, NextMarker: aws.String(firstToken)}, nil
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					g.Expect(aws.ToString(input.Marker)).To(Equal(firstToken))
					return &route53.ListHostedZonesOutput{IsTruncated: true, NextMarker: aws.String(secondToken)}, nil
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					g.Expect(aws.ToString(input.Marker)).To(Equal(secondToken))
					return &route53.ListHostedZonesOutput{IsTruncated: true, NextMarker: aws.String(firstToken)}, nil
				}),
		)

		o := &CreateInfraOptions{Region: "us-east-1"}
		id, err := o.CreatePrivateZone(cancelledCtx(), logr.Discard(), mockR53, testZoneName, testVPCID, false, mockR53, "")
		g.Expect(err).To(MatchError("failed to create hosted zone: duplicate pagination token"))
		g.Expect(err.Error()).NotTo(ContainSubstring(firstToken))
		g.Expect(err.Error()).NotTo(ContainSubstring(secondToken))
		g.Expect(err.Error()).NotTo(ContainSubstring(callerReference))
		g.Expect(id).To(BeEmpty())
	})

	t.Run("When a cross-account successful response is lost, it should recover only the zone with the same caller reference", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		mockVPCOwner := awsapi.NewMockROUTE53API(ctrl)
		var callerReference string

		gomock.InOrder(
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(emptyZonePage(), nil),
			mockR53.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
					g.Expect(aws.ToString(input.VPC.VPCId)).To(Equal(testInitialVPC))
					callerReference = aws.ToString(input.CallerReference)
					return nil, &route53types.HostedZoneAlreadyExists{Message: aws.String("already created")}
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					page := privateZonePage("UNRELATEDZONE", testZoneName)
					page.HostedZones[0].CallerReference = aws.String("unrelated-caller-reference")
					recoveredZone := privateZonePage("RECOVEREDZONE", testZoneName).HostedZones[0]
					recoveredZone.CallerReference = aws.String(callerReference)
					page.HostedZones = append(page.HostedZones, recoveredZone)
					return page, nil
				}),
			mockR53.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
					g.Expect(aws.ToString(input.VPCId)).To(Equal(testInitialVPC))
					g.Expect(input.VPCRegion).To(Equal(route53types.VPCRegionUsEast1))
					return hostedZonesForVPC(), nil
				}),
			mockR53.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
					g.Expect(aws.ToString(input.CallerReference)).To(Equal(callerReference))
					return nil, &route53types.HostedZoneAlreadyExists{Message: aws.String("already created")}
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					page := privateZonePage("RECOVEREDZONE", testZoneName)
					page.HostedZones[0].CallerReference = aws.String(callerReference)
					return page, nil
				}),
			mockR53.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
					g.Expect(aws.ToString(input.VPCId)).To(Equal(testInitialVPC))
					g.Expect(input.VPCRegion).To(Equal(route53types.VPCRegionUsEast1))
					return hostedZonesForVPC("RECOVEREDZONE"), nil
				}),
			mockR53.EXPECT().ChangeTagsForResource(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ChangeTagsForResourceOutput{}, nil),
			mockR53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(soaRecordFor(testZoneName), nil),
			mockR53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ChangeResourceRecordSetsOutput{}, nil),
			mockR53.EXPECT().CreateVPCAssociationAuthorization(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.CreateVPCAssociationAuthorizationOutput{}, nil),
			mockVPCOwner.EXPECT().AssociateVPCWithHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.AssociateVPCWithHostedZoneOutput{}, nil),
			mockR53.EXPECT().DisassociateVPCFromHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.DisassociateVPCFromHostedZoneOutput{}, nil),
		)

		o := &CreateInfraOptions{Region: "us-east-1"}
		id, err := o.CreatePrivateZone(t.Context(), logr.Discard(), mockR53, testZoneName, testVPCID, true, mockVPCOwner, testInitialVPC)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(id).To(Equal("RECOVEREDZONE"))
	})

	t.Run("When Route53 normalizes a zone name, it should recover the zone for the same caller reference", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		mixedCaseZoneName := "Internal.Example.COM"
		var callerReference string

		gomock.InOrder(
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(emptyZonePage(), nil),
			mockR53.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
					callerReference = aws.ToString(input.CallerReference)
					return nil, &route53types.HostedZoneAlreadyExists{Message: aws.String("already created")}
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					page := privateZonePage("RECOVEREDZONE", strings.ToLower(mixedCaseZoneName))
					page.HostedZones[0].CallerReference = aws.String(callerReference)
					return page, nil
				}),
			mockR53.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
					g.Expect(aws.ToString(input.VPCId)).To(Equal(testVPCID))
					g.Expect(input.VPCRegion).To(Equal(route53types.VPCRegionUsEast1))
					return hostedZonesForVPC("RECOVEREDZONE"), nil
				}),
			mockR53.EXPECT().ChangeTagsForResource(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ChangeTagsForResourceOutput{}, nil),
			mockR53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(soaRecordFor(strings.ToLower(mixedCaseZoneName)), nil),
			mockR53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53.ChangeResourceRecordSetsOutput{}, nil),
		)

		o := &CreateInfraOptions{Region: "us-east-1"}
		id, err := o.CreatePrivateZone(t.Context(), logr.Discard(), mockR53, mixedCaseZoneName, testVPCID, false, mockR53, "")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(id).To(Equal("RECOVEREDZONE"))
	})

	t.Run("When a duplicate-response zone is not associated with the requested VPC, it should return a retryable association error without exposing the zone ID", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		var callerReference string

		gomock.InOrder(
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(emptyZonePage(), nil),
			mockR53.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
					callerReference = aws.ToString(input.CallerReference)
					return nil, &route53types.HostedZoneAlreadyExists{Message: aws.String("already created")}
				}),
			mockR53.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
					page := privateZonePage("UNRELATEDZONE", testZoneName)
					page.HostedZones[0].CallerReference = aws.String(callerReference)
					return page, nil
				}),
			mockR53.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(hostedZonesForVPC("OTHERZONE"), nil),
		)

		o := &CreateInfraOptions{Region: "us-east-1"}
		id, err := o.CreatePrivateZone(cancelledCtx(), logr.Discard(), mockR53, testZoneName, testVPCID, false, mockR53, "")
		g.Expect(err).To(MatchError(ContainSubstring("cannot yet verify hosted zone VPC association")))
		g.Expect(err.Error()).NotTo(ContainSubstring("UNRELATEDZONE"))
		g.Expect(err.Error()).NotTo(ContainSubstring(callerReference))
		g.Expect(id).To(BeEmpty())
	})
}

func TestRoute53VPCMatchingHostedZone(t *testing.T) {
	t.Run("When a pagination token repeats non-consecutively, it should return a redacted duplicate-token error", func(t *testing.T) {
		g := NewWithT(t)
		ctrl := gomock.NewController(t)
		mockR53 := awsapi.NewMockROUTE53API(ctrl)
		firstToken := "opaque-vpc-token-a"
		secondToken := "opaque-vpc-token-b"

		gomock.InOrder(
			mockR53.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
					g.Expect(input.NextToken).To(BeNil())
					return &route53.ListHostedZonesByVPCOutput{NextToken: aws.String(firstToken)}, nil
				}),
			mockR53.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
					g.Expect(aws.ToString(input.NextToken)).To(Equal(firstToken))
					return &route53.ListHostedZonesByVPCOutput{NextToken: aws.String(secondToken)}, nil
				}),
			mockR53.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, input *route53.ListHostedZonesByVPCInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByVPCOutput, error) {
					g.Expect(aws.ToString(input.NextToken)).To(Equal(secondToken))
					return &route53.ListHostedZonesByVPCOutput{NextToken: aws.String(firstToken)}, nil
				}),
		)

		_, err := route53VPCMatchingHostedZone(t.Context(), mockR53, &route53types.VPC{
			VPCId:     aws.String(testVPCID),
			VPCRegion: route53types.VPCRegionUsEast1,
		}, map[string]struct{}{})
		g.Expect(err).To(MatchError("duplicate pagination token"))
		g.Expect(err.Error()).NotTo(ContainSubstring(firstToken))
		g.Expect(err.Error()).NotTo(ContainSubstring(secondToken))
	})
}

func TestCleanupPublicZone(t *testing.T) {
	tests := []struct {
		name             string
		redact           bool
		setupMock        func(*awsapi.MockROUTE53API)
		expectError      bool
		errorContains    string
		useCtx           func() context.Context
		wantLogRedacted  bool
		wantLogSubstring string
	}{
		{
			name: "When zone and wildcard record exist it should delete the record and return nil",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(publicZonePage("PUBZONE", testBaseDomain), nil)
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{
								Name: aws.String("*.apps." + testCluster + "." + testBaseDomain + "."),
								Type: route53types.RRTypeA,
							},
						},
					}, nil)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ChangeResourceRecordSetsOutput{}, nil)
			},
		},
		{
			name:   "When zone and wildcard record exist with redact enabled it should delete the record and redact the domain in logs",
			redact: true,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(publicZonePage("PUBZONE", testBaseDomain), nil)
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{
								Name: aws.String("*.apps." + testCluster + "." + testBaseDomain + "."),
								Type: route53types.RRTypeA,
							},
						},
					}, nil)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ChangeResourceRecordSetsOutput{}, nil)
			},
			wantLogRedacted:  true,
			wantLogSubstring: "[REDACTED]",
		},
		{
			name: "When the zone is not found it should return nil as a no-op",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(emptyZonePage(), nil)
			},
		},
		{
			name: "When the wildcard record is not found it should ignore the error and return nil",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(publicZonePage("PUBZONE", testBaseDomain), nil)
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{},
					}, nil)
			},
		},
		{
			name:   "When LookupZone fails with a non-not-found error, it should return a wrapped error",
			useCtx: cancelledCtx,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("throttling exception"))
			},
			expectError:   true,
			errorContains: "failed to lookup public hosted zone",
		},
		{
			name: "When ChangeResourceRecordSets fails with a non-404 error, it should return the error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(publicZonePage("PUBZONE", testBaseDomain), nil)
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{
								Name: aws.String("*.apps." + testCluster + "." + testBaseDomain + "."),
								Type: route53types.RRTypeA,
							},
						},
					}, nil)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("permission denied"))
			},
			expectError:   true,
			errorContains: "failed to delete wildcard record",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			ctx := t.Context()
			if tt.useCtx != nil {
				ctx = tt.useCtx()
			}

			var logOutput strings.Builder
			logger := logr.Discard()
			if tt.wantLogRedacted {
				logger = funcr.New(func(prefix, args string) {
					logOutput.WriteString(args)
				}, funcr.Options{})
			}

			o := &DestroyInfraOptions{
				BaseDomain:       testBaseDomain,
				Name:             testCluster,
				RedactBaseDomain: tt.redact,
				Log:              logger,
			}
			err := o.CleanupPublicZone(ctx, mockR53)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err).To(MatchError(ContainSubstring(tt.errorContains)))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tt.wantLogRedacted {
				g.Expect(logOutput.String()).To(ContainSubstring(tt.wantLogSubstring))
				g.Expect(logOutput.String()).ToNot(ContainSubstring(testBaseDomain))
			}
		})
	}
}

func TestDestroyDNS(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockROUTE53API)
		expectError   bool
		errorContains string
	}{
		{
			name: "When zone exists but wildcard record is absent it should return no errors",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(publicZonePage("PUBZONE", testBaseDomain), nil)
				// wildcard record not found — CleanupPublicZone treats this as a no-op
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{},
					}, nil)
			},
		},
		{
			name: "When CleanupPublicZone fails, it should return the error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(publicZonePage("PUBZONE", testBaseDomain), nil)
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{
								Name: aws.String("*.apps." + testCluster + "." + testBaseDomain + "."),
								Type: route53types.RRTypeA,
							},
						},
					}, nil)
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("permission denied"))
			},
			expectError:   true,
			errorContains: "failed to delete wildcard record",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			o := &DestroyInfraOptions{
				BaseDomain: testBaseDomain,
				Name:       testCluster,
				Log:        logr.Discard(),
			}
			errs := o.DestroyDNS(context.Background(), mockR53)

			if tt.expectError {
				g.Expect(errs).To(ContainElement(HaveOccurred()))
				if tt.errorContains != "" {
					g.Expect(errs).To(ContainElement(MatchError(ContainSubstring(tt.errorContains))))
				}
			} else {
				g.Expect(errs).To(HaveEach(BeNil()))
			}
		})
	}
}

func TestDestroyPrivateZones(t *testing.T) {
	tests := []struct {
		name          string
		setupListMock func(*awsapi.MockROUTE53API)
		setupRecsMock func(*awsapi.MockROUTE53API)
		expectError   bool
		errorContains string
		useCtx        func() context.Context
	}{
		{
			name: "When private zones exist it should delete them and return no errors",
			setupListMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListHostedZonesByVPCOutput{
						HostedZoneSummaries: []route53types.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/PRIVZONE"),
								Name:         aws.String(testZoneName + "."),
							},
						},
					}, nil)
			},
			setupRecsMock: func(m *awsapi.MockROUTE53API) {
				// deleteRecords: no deletable records
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{},
					}, nil)
				m.EXPECT().DeleteHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.DeleteHostedZoneOutput{}, nil)
			},
		},
		{
			name: "When no private zones exist it should return no errors",
			setupListMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListHostedZonesByVPCOutput{
						HostedZoneSummaries: []route53types.HostedZoneSummary{},
					}, nil)
			},
			setupRecsMock: func(_ *awsapi.MockROUTE53API) {},
		},
		{
			name:   "When ListHostedZonesByVPC fails, it should return the error",
			useCtx: cancelledCtx,
			setupListMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("list failed"))
			},
			setupRecsMock: func(_ *awsapi.MockROUTE53API) {},
			expectError:   true,
			errorContains: "failed to list hosted zones for vpc",
		},
		{
			name: "When deleteZone fails, it should return the error",
			setupListMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZonesByVPC(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&route53.ListHostedZonesByVPCOutput{
						HostedZoneSummaries: []route53types.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/PRIVZONE"),
								Name:         aws.String(testZoneName + "."),
							},
						},
					}, nil)
			},
			setupRecsMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("records error"))
			},
			expectError:   true,
			errorContains: "failed to delete private hosted zones for vpc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			mockListClient := awsapi.NewMockROUTE53API(ctrl)
			mockRecsClient := awsapi.NewMockROUTE53API(ctrl)
			tt.setupListMock(mockListClient)
			tt.setupRecsMock(mockRecsClient)

			ctx := context.Background()
			if tt.useCtx != nil {
				ctx = tt.useCtx()
			}

			o := &DestroyInfraOptions{Region: "us-east-1", Log: logr.Discard()}
			errs := o.DestroyPrivateZones(ctx, mockListClient, mockRecsClient, testVPCID)

			if tt.expectError {
				g.Expect(errs).To(ContainElement(HaveOccurred()))
				if tt.errorContains != "" {
					g.Expect(errs).To(ContainElement(MatchError(ContainSubstring(tt.errorContains))))
				}
			} else {
				g.Expect(errs).To(BeEmpty())
			}
		})
	}
}
