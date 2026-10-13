package aws

import (
	"context"
	"errors"
	"strings"
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
			name:       "When the zone API call fails it should return an error",
			baseDomain: testBaseDomain,
			useCtx:     cancelledCtx,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("api error"))
			},
			expectError: true,
		},
		{
			name:       "When redact is true and zone lookup fails it should return error without logging the domain",
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
		useCtx            func() context.Context
	}{
		{
			name:     "When the private zone already exists it should update the SOA minimum and return its ID",
			zoneName: testZoneName,
			vpcID:    testVPCID,
			setupMock: func(m *awsapi.MockROUTE53API) {
				// LookupZone finds the existing zone
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(privateZonePage("EXISTZONE", testZoneName), nil)
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
			name:     "When the private zone does not exist it should create it and return the new ID",
			zoneName: testZoneName,
			vpcID:    testVPCID,
			setupMock: func(m *awsapi.MockROUTE53API) {
				// LookupZone finds no zone
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
			name:     "When CreateHostedZone fails it should return a wrapped error",
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
			name:     "When setSOAMinimum fails on an existing zone it should return an error",
			zoneName: testZoneName,
			vpcID:    testVPCID,
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(privateZonePage("EXISTZONE", testZoneName), nil)
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
}

func TestCleanupPublicZone(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockROUTE53API)
		expectError   bool
		errorContains string
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
			name: "When ChangeResourceRecordSets fails with a non-404 error it should return the error",
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
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			o := &DestroyInfraOptions{
				BaseDomain: testBaseDomain,
				Name:       testCluster,
				Log:        logr.Discard(),
			}
			err := o.CleanupPublicZone(context.Background(), mockR53)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
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
			name: "When CleanupPublicZone fails it should return the error",
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
			name:   "When ListHostedZonesByVPC fails it should return the error",
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
			name: "When deleteZone fails it should return the error",
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
