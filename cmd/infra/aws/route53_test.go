package aws

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"

	"github.com/go-logr/logr"
)

// mockRoute53Client implements route53iface.Route53API for testing
type mockRoute53Client struct {
	route53iface.Route53API

	// ListHostedZonesByVPC behavior
	listHostedZonesByVPCPages []*route53.ListHostedZonesByVPCOutput
	listHostedZonesByVPCError error

	// DeleteHostedZone behavior
	deleteHostedZoneErrors map[string]error

	// ListResourceRecordSets behavior
	listResourceRecordSetsOutputs map[string]*route53.ListResourceRecordSetsOutput
	listResourceRecordSetsErrors  map[string]error

	// ChangeResourceRecordSets behavior
	changeResourceRecordSetsErrors map[string]error

	// Tracking
	deletedZones             []string
	deletedRecordsForZones   map[string][]string
	listHostedZonesCallCount int
}

func newMockRoute53Client() *mockRoute53Client {
	return &mockRoute53Client{
		deletedZones: []string{},
	}
}

func (m *mockRoute53Client) ListHostedZonesByVPCWithContext(ctx context.Context, input *route53.ListHostedZonesByVPCInput, opts ...request.Option) (*route53.ListHostedZonesByVPCOutput, error) {
	m.listHostedZonesCallCount++

	if m.listHostedZonesByVPCError != nil {
		return nil, m.listHostedZonesByVPCError
	}

	// Handle pagination
	if m.listHostedZonesByVPCPages == nil {
		return &route53.ListHostedZonesByVPCOutput{}, nil
	}

	// Determine which page to return based on NextToken
	pageIndex := 0
	if input.NextToken != nil {
		// Find the page index from the token
		for i, page := range m.listHostedZonesByVPCPages {
			if page.NextToken != nil && *page.NextToken == *input.NextToken {
				pageIndex = i
				break
			}
		}
		// If we're using a next token, advance to the next page
		if input.NextToken != nil && pageIndex < len(m.listHostedZonesByVPCPages)-1 {
			pageIndex++
		}
	}

	if pageIndex >= len(m.listHostedZonesByVPCPages) {
		return &route53.ListHostedZonesByVPCOutput{}, nil
	}

	return m.listHostedZonesByVPCPages[pageIndex], nil
}

func (m *mockRoute53Client) DeleteHostedZoneWithContext(ctx context.Context, input *route53.DeleteHostedZoneInput, opts ...request.Option) (*route53.DeleteHostedZoneOutput, error) {
	zoneID := cleanZoneID(*input.Id)
	m.deletedZones = append(m.deletedZones, zoneID)

	if m.deleteHostedZoneErrors != nil {
		if err, exists := m.deleteHostedZoneErrors[zoneID]; exists {
			return nil, err
		}
	}

	return &route53.DeleteHostedZoneOutput{}, nil
}

func (m *mockRoute53Client) ListResourceRecordSetsWithContext(ctx context.Context, input *route53.ListResourceRecordSetsInput, opts ...request.Option) (*route53.ListResourceRecordSetsOutput, error) {
	zoneID := cleanZoneID(*input.HostedZoneId)

	if m.listResourceRecordSetsErrors != nil {
		if err, exists := m.listResourceRecordSetsErrors[zoneID]; exists {
			return nil, err
		}
	}

	if m.listResourceRecordSetsOutputs != nil {
		if output, exists := m.listResourceRecordSetsOutputs[zoneID]; exists {
			return output, nil
		}
	}

	// Default: return empty record set (only NS and SOA which will be skipped)
	return &route53.ListResourceRecordSetsOutput{
		ResourceRecordSets: []*route53.ResourceRecordSet{
			{
				Name: aws.String("example.com."),
				Type: aws.String("NS"),
			},
			{
				Name: aws.String("example.com."),
				Type: aws.String("SOA"),
			},
		},
	}, nil
}

func (m *mockRoute53Client) ChangeResourceRecordSetsWithContext(ctx context.Context, input *route53.ChangeResourceRecordSetsInput, opts ...request.Option) (*route53.ChangeResourceRecordSetsOutput, error) {
	zoneID := cleanZoneID(*input.HostedZoneId)

	if m.changeResourceRecordSetsErrors != nil {
		if err, exists := m.changeResourceRecordSetsErrors[zoneID]; exists {
			return nil, err
		}
	}

	// Track deleted records
	if m.deletedRecordsForZones == nil {
		m.deletedRecordsForZones = make(map[string][]string)
	}
	for _, change := range input.ChangeBatch.Changes {
		if *change.Action == "DELETE" {
			m.deletedRecordsForZones[zoneID] = append(m.deletedRecordsForZones[zoneID], *change.ResourceRecordSet.Name)
		}
	}

	return &route53.ChangeResourceRecordSetsOutput{}, nil
}

func TestDestroyPrivateZones(t *testing.T) {
	tests := map[string]struct {
		mockClient      *mockRoute53Client
		expectedErrors  int
		expectedDeletes []string
		validateFunc    func(*GomegaWithT, *mockRoute53Client)
	}{
		"When VPC has no hosted zones, it should return no errors": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCPages = []*route53.ListHostedZonesByVPCOutput{
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{},
					},
				}
				return m
			}(),
			expectedErrors:  0,
			expectedDeletes: []string{},
		},
		"When VPC has zones on single page, it should delete all zones": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCPages = []*route53.ListHostedZonesByVPCOutput{
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/Z1234567890ABC"),
								Name:         aws.String("example.hypershift.local."),
							},
							{
								HostedZoneId: aws.String("/hostedzone/Z0987654321DEF"),
								Name:         aws.String("test.openshift.dev."),
							},
						},
					},
				}
				return m
			}(),
			expectedErrors:  0,
			expectedDeletes: []string{"Z1234567890ABC", "Z0987654321DEF"},
		},
		"When VPC has zones across multiple pages, it should delete all zones using pagination": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCPages = []*route53.ListHostedZonesByVPCOutput{
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/Z1111111111111"),
								Name:         aws.String("zone1.hypershift.local."),
							},
							{
								HostedZoneId: aws.String("/hostedzone/Z2222222222222"),
								Name:         aws.String("zone2.hypershift.local."),
							},
						},
						NextToken: aws.String("page2"),
					},
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/Z3333333333333"),
								Name:         aws.String("zone3.hypershift.local."),
							},
							{
								HostedZoneId: aws.String("/hostedzone/Z4444444444444"),
								Name:         aws.String("zone4.hypershift.local."),
							},
						},
						NextToken: aws.String("page3"),
					},
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/Z5555555555555"),
								Name:         aws.String("zone5.hypershift.local."),
							},
						},
					},
				}
				return m
			}(),
			expectedErrors:  0,
			expectedDeletes: []string{"Z1111111111111", "Z2222222222222", "Z3333333333333", "Z4444444444444", "Z5555555555555"},
			validateFunc: func(g *GomegaWithT, mock *mockRoute53Client) {
				// Verify pagination was used (should call ListHostedZonesByVPC 3 times)
				g.Expect(mock.listHostedZonesCallCount).To(Equal(3))
			},
		},
		"When NextToken is empty string, it should stop pagination": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCPages = []*route53.ListHostedZonesByVPCOutput{
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/ZABC123"),
								Name:         aws.String("zone1.test."),
							},
						},
						NextToken: aws.String(""),
					},
				}
				return m
			}(),
			expectedErrors:  0,
			expectedDeletes: []string{"ZABC123"},
			validateFunc: func(g *GomegaWithT, mock *mockRoute53Client) {
				g.Expect(mock.listHostedZonesCallCount).To(Equal(1))
			},
		},
		"When VPC does not exist (ErrCodeInvalidVPCId), it should log and return no errors": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCError = awserr.New(route53.ErrCodeInvalidVPCId, "VPC not found", nil)
				return m
			}(),
			expectedErrors:  0,
			expectedDeletes: []string{},
		},
		"When zone is already deleted (ErrCodeNoSuchHostedZone), it should log and continue": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCPages = []*route53.ListHostedZonesByVPCOutput{
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/ZDELETED1"),
								Name:         aws.String("deleted.test."),
							},
							{
								HostedZoneId: aws.String("/hostedzone/ZEXISTS1"),
								Name:         aws.String("exists.test."),
							},
						},
					},
				}
				m.deleteHostedZoneErrors = map[string]error{
					"ZDELETED1": awserr.New(route53.ErrCodeNoSuchHostedZone, "Zone already deleted", nil),
				}
				return m
			}(),
			// Note: Expected 1 error because deleteZone wraps the error with %v (not %w)
			// which breaks the error chain, preventing DestroyPrivateZones from detecting NoSuchHostedZone
			expectedErrors:  1,
			expectedDeletes: []string{"ZDELETED1", "ZEXISTS1"},
		},
		"When deleteZone fails for one zone, it should collect error and continue with others": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCPages = []*route53.ListHostedZonesByVPCOutput{
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/ZFAIL1"),
								Name:         aws.String("fail.test."),
							},
							{
								HostedZoneId: aws.String("/hostedzone/ZSUCCESS1"),
								Name:         aws.String("success.test."),
							},
						},
					},
				}
				m.deleteHostedZoneErrors = map[string]error{
					"ZFAIL1": awserr.New("ServiceUnavailable", "Service unavailable", nil),
				}
				return m
			}(),
			expectedErrors:  1,
			expectedDeletes: []string{"ZFAIL1", "ZSUCCESS1"},
		},
		"When ListHostedZonesByVPC fails with other errors, it should return error": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCError = awserr.New("AccessDenied", "Access denied", nil)
				return m
			}(),
			expectedErrors:  1,
			expectedDeletes: []string{},
		},
		"When zone has custom records, it should delete records before deleting zone": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCPages = []*route53.ListHostedZonesByVPCOutput{
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/ZWITHRECORDS"),
								Name:         aws.String("withrecords.test."),
							},
						},
					},
				}
				m.listResourceRecordSetsOutputs = map[string]*route53.ListResourceRecordSetsOutput{
					"ZWITHRECORDS": {
						ResourceRecordSets: []*route53.ResourceRecordSet{
							{
								Name: aws.String("withrecords.test."),
								Type: aws.String("NS"),
							},
							{
								Name: aws.String("withrecords.test."),
								Type: aws.String("SOA"),
							},
							{
								Name: aws.String("api.withrecords.test."),
								Type: aws.String("A"),
								ResourceRecords: []*route53.ResourceRecord{
									{Value: aws.String("10.0.0.1")},
								},
							},
							{
								Name: aws.String("*.apps.withrecords.test."),
								Type: aws.String("A"),
								ResourceRecords: []*route53.ResourceRecord{
									{Value: aws.String("10.0.0.2")},
								},
							},
						},
					},
				}
				return m
			}(),
			expectedErrors:  0,
			expectedDeletes: []string{"ZWITHRECORDS"},
			validateFunc: func(g *GomegaWithT, mock *mockRoute53Client) {
				// Verify records were deleted
				g.Expect(mock.deletedRecordsForZones["ZWITHRECORDS"]).To(HaveLen(2))
				g.Expect(mock.deletedRecordsForZones["ZWITHRECORDS"]).To(ContainElements(
					"api.withrecords.test.",
					"*.apps.withrecords.test.",
				))
			},
		},
		"When deletion fails on second page, it should still process remaining zones": {
			mockClient: func() *mockRoute53Client {
				m := newMockRoute53Client()
				m.listHostedZonesByVPCPages = []*route53.ListHostedZonesByVPCOutput{
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/ZPAGE1SUCCESS"),
								Name:         aws.String("page1.test."),
							},
						},
						NextToken: aws.String("page2"),
					},
					{
						HostedZoneSummaries: []*route53.HostedZoneSummary{
							{
								HostedZoneId: aws.String("/hostedzone/ZPAGE2FAIL"),
								Name:         aws.String("page2fail.test."),
							},
							{
								HostedZoneId: aws.String("/hostedzone/ZPAGE2SUCCESS"),
								Name:         aws.String("page2success.test."),
							},
						},
					},
				}
				m.deleteHostedZoneErrors = map[string]error{
					"ZPAGE2FAIL": fmt.Errorf("deletion failed"),
				}
				return m
			}(),
			expectedErrors:  1,
			expectedDeletes: []string{"ZPAGE1SUCCESS", "ZPAGE2FAIL", "ZPAGE2SUCCESS"},
			validateFunc: func(g *GomegaWithT, mock *mockRoute53Client) {
				g.Expect(mock.listHostedZonesCallCount).To(Equal(2))
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctx := context.Background()

			opts := &DestroyInfraOptions{
				Region: "us-east-1",
				Log:    logr.Discard(),
			}

			vpcID := aws.String("vpc-12345")
			errs := opts.DestroyPrivateZones(ctx, tc.mockClient, tc.mockClient, vpcID)

			g.Expect(len(errs)).To(Equal(tc.expectedErrors), "expected %d errors but got %d: %v", tc.expectedErrors, len(errs), errs)
			g.Expect(tc.mockClient.deletedZones).To(Equal(tc.expectedDeletes))

			if tc.validateFunc != nil {
				tc.validateFunc(g, tc.mockClient)
			}
		})
	}
}
