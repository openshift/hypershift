package aws

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
)

type testAPIError struct {
	code string
}

func (e *testAPIError) Error() string                 { return fmt.Sprintf("api error %s", e.code) }
func (e *testAPIError) ErrorCode() string             { return e.code }
func (e *testAPIError) ErrorMessage() string          { return e.code }
func (e *testAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

func TestIsRetriableVPCEndpointError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is invalidRouteTableID, it should be retriable",
			err:      &testAPIError{code: invalidRouteTableID},
			expected: true,
		},
		{
			name:     "When error is RequestLimitExceeded, it should be retriable",
			err:      &testAPIError{code: "RequestLimitExceeded"},
			expected: true,
		},
		{
			name:     "When error is Throttling, it should be retriable",
			err:      &testAPIError{code: "Throttling"},
			expected: true,
		},
		{
			name:     "When error is EC2ThrottledException, it should be retriable",
			err:      &testAPIError{code: "EC2ThrottledException"},
			expected: true,
		},
		{
			name:     "When error is a non-retriable API error, it should not be retriable",
			err:      &testAPIError{code: "InvalidParameterValue"},
			expected: false,
		},
		{
			name:     "When error is not an API error, it should not be retriable",
			err:      fmt.Errorf("network timeout"),
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(isRetriableVPCEndpointError(tt.err)).To(Equal(tt.expected))
		})
	}
}

func createEC2Options() *CreateInfraOptions {
	return &CreateInfraOptions{
		InfraID: testInfraID,
		Region:  "us-east-1",
	}
}

func invalidRouteTableIDError() error {
	return &smithy.GenericAPIError{
		Code:    "InvalidRouteTableId.NotFound",
		Message: "The routeTable ID 'rtb-xxx' does not exist",
	}
}

func TestCreatePrivateRouteTable(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockEC2API)
		expectError   bool
		errorContains string
	}{
		{
			name: "When route table exists and routes are already configured it should return the table ID without creating routes",
			setupMock: func(m *awsapi.MockEC2API) {
				existingRT := &ec2types.RouteTable{
					RouteTableId: aws.String("rtb-existing"),
					Routes: []ec2types.Route{
						{
							NatGatewayId:         aws.String("nat-123"),
							DestinationCidrBlock: aws.String("0.0.0.0/0"),
						},
					},
				}
				m.EXPECT().DescribeRouteTables(gomock.Any(), gomock.Any()).
					Return(&ec2.DescribeRouteTablesOutput{RouteTables: []ec2types.RouteTable{*existingRT}}, nil)
				m.EXPECT().AssociateRouteTable(gomock.Any(), gomock.Any()).
					Return(&ec2.AssociateRouteTableOutput{}, nil)
			},
			expectError: false,
		},
		{
			name: "When CreateRoute fails with InvalidRouteTableId.NotFound it should retry until success",
			setupMock: func(m *awsapi.MockEC2API) {
				newRT := &ec2types.RouteTable{
					RouteTableId: aws.String("rtb-new"),
					Routes:       []ec2types.Route{},
				}
				gomock.InOrder(
					m.EXPECT().DescribeRouteTables(gomock.Any(), gomock.Any()).
						Return(&ec2.DescribeRouteTablesOutput{}, nil),
					m.EXPECT().CreateRouteTable(gomock.Any(), gomock.Any()).
						Return(&ec2.CreateRouteTableOutput{RouteTable: newRT}, nil),
					m.EXPECT().CreateRoute(gomock.Any(), gomock.Any()).
						Return(nil, invalidRouteTableIDError()),
					m.EXPECT().CreateRoute(gomock.Any(), gomock.Any()).
						Return(&ec2.CreateRouteOutput{}, nil),
					m.EXPECT().AssociateRouteTable(gomock.Any(), gomock.Any()).
						Return(&ec2.AssociateRouteTableOutput{}, nil),
				)
			},
			expectError: false,
		},
		{
			name: "When CreateRoute fails with InvalidNatGatewayID.NotFound it should retry until success",
			setupMock: func(m *awsapi.MockEC2API) {
				newRT := &ec2types.RouteTable{
					RouteTableId: aws.String("rtb-new"),
					Routes:       []ec2types.Route{},
				}
				gomock.InOrder(
					m.EXPECT().DescribeRouteTables(gomock.Any(), gomock.Any()).
						Return(&ec2.DescribeRouteTablesOutput{}, nil),
					m.EXPECT().CreateRouteTable(gomock.Any(), gomock.Any()).
						Return(&ec2.CreateRouteTableOutput{RouteTable: newRT}, nil),
					m.EXPECT().CreateRoute(gomock.Any(), gomock.Any()).
						Return(nil, &smithy.GenericAPIError{
							Code:    "InvalidNatGatewayID.NotFound",
							Message: "The NAT gateway ID does not exist",
						}),
					m.EXPECT().CreateRoute(gomock.Any(), gomock.Any()).
						Return(&ec2.CreateRouteOutput{}, nil),
					m.EXPECT().AssociateRouteTable(gomock.Any(), gomock.Any()).
						Return(&ec2.AssociateRouteTableOutput{}, nil),
				)
			},
			expectError: false,
		},
		{
			name: "When CreateRoute fails with non-retriable error it should not retry",
			setupMock: func(m *awsapi.MockEC2API) {
				newRT := &ec2types.RouteTable{
					RouteTableId: aws.String("rtb-new"),
					Routes:       []ec2types.Route{},
				}
				nonRetriableError := &smithy.GenericAPIError{
					Code:    "InvalidParameterValue",
					Message: "Invalid parameter",
				}
				gomock.InOrder(
					m.EXPECT().DescribeRouteTables(gomock.Any(), gomock.Any()).
						Return(&ec2.DescribeRouteTablesOutput{}, nil),
					m.EXPECT().CreateRouteTable(gomock.Any(), gomock.Any()).
						Return(&ec2.CreateRouteTableOutput{RouteTable: newRT}, nil),
					m.EXPECT().CreateRoute(gomock.Any(), gomock.Any()).
						Return(nil, nonRetriableError).Times(1),
				)
			},
			expectError:   true,
			errorContains: "cannot create nat gateway route",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockEC2 := awsapi.NewMockEC2API(ctrl)
			tc.setupMock(mockEC2)

			opts := createEC2Options()
			logger := logr.Discard()
			ctx := context.Background()

			routeTableID, err := opts.CreatePrivateRouteTable(
				ctx,
				logger,
				mockEC2,
				"vpc-123",
				"nat-123",
				"subnet-123",
				"us-east-1a",
			)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(routeTableID).NotTo(BeEmpty())
			}
		})
	}
}

func TestCreatePublicRouteTable(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockEC2API)
		expectError   bool
		errorContains string
	}{
		{
			name: "When CreateRoute for internet gateway fails with InvalidRouteTableId.NotFound it should retry until success",
			setupMock: func(m *awsapi.MockEC2API) {
				newRT := &ec2types.RouteTable{
					RouteTableId: aws.String("rtb-new"),
					Routes:       []ec2types.Route{},
				}
				mainRT := &ec2types.RouteTable{
					RouteTableId: aws.String("rtb-main"),
					Associations: []ec2types.RouteTableAssociation{
						{
							Main:                    aws.Bool(true),
							RouteTableAssociationId: aws.String("rtbassoc-main"),
						},
					},
				}
				gomock.InOrder(
					m.EXPECT().DescribeRouteTables(gomock.Any(), gomock.Any()).
						Return(&ec2.DescribeRouteTablesOutput{}, nil),
					m.EXPECT().CreateRouteTable(gomock.Any(), gomock.Any()).
						Return(&ec2.CreateRouteTableOutput{RouteTable: newRT}, nil),
					m.EXPECT().DescribeRouteTables(gomock.Any(), gomock.Any()).
						Return(&ec2.DescribeRouteTablesOutput{RouteTables: []ec2types.RouteTable{*mainRT}}, nil),
					m.EXPECT().ReplaceRouteTableAssociation(gomock.Any(), gomock.Any()).
						Return(&ec2.ReplaceRouteTableAssociationOutput{}, nil),
					m.EXPECT().CreateRoute(gomock.Any(), gomock.Any()).
						Return(nil, invalidRouteTableIDError()),
					m.EXPECT().CreateRoute(gomock.Any(), gomock.Any()).
						Return(&ec2.CreateRouteOutput{}, nil),
					m.EXPECT().AssociateRouteTable(gomock.Any(), gomock.Any()).
						Return(&ec2.AssociateRouteTableOutput{}, nil).Times(2),
				)
			},
			expectError: false,
		},
		{
			name: "When CreateRoute for internet gateway fails with non-retriable InvalidParameterValue error it should not retry",
			setupMock: func(m *awsapi.MockEC2API) {
				newRT := &ec2types.RouteTable{
					RouteTableId: aws.String("rtb-new"),
					Routes:       []ec2types.Route{},
				}
				mainRT := &ec2types.RouteTable{
					RouteTableId: aws.String("rtb-main"),
					Associations: []ec2types.RouteTableAssociation{
						{
							Main:                    aws.Bool(true),
							RouteTableAssociationId: aws.String("rtbassoc-main"),
						},
					},
				}
				nonRetriableError := &smithy.GenericAPIError{
					Code:    "InvalidParameterValue",
					Message: "Invalid parameter",
				}
				gomock.InOrder(
					m.EXPECT().DescribeRouteTables(gomock.Any(), gomock.Any()).
						Return(&ec2.DescribeRouteTablesOutput{}, nil),
					m.EXPECT().CreateRouteTable(gomock.Any(), gomock.Any()).
						Return(&ec2.CreateRouteTableOutput{RouteTable: newRT}, nil),
					m.EXPECT().DescribeRouteTables(gomock.Any(), gomock.Any()).
						Return(&ec2.DescribeRouteTablesOutput{RouteTables: []ec2types.RouteTable{*mainRT}}, nil),
					m.EXPECT().ReplaceRouteTableAssociation(gomock.Any(), gomock.Any()).
						Return(&ec2.ReplaceRouteTableAssociationOutput{}, nil),
					m.EXPECT().CreateRoute(gomock.Any(), gomock.Any()).
						Return(nil, nonRetriableError).Times(1),
				)
			},
			expectError:   true,
			errorContains: "cannot create route to internet gateway",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockEC2 := awsapi.NewMockEC2API(ctrl)
			tc.setupMock(mockEC2)

			opts := createEC2Options()
			logger := logr.Discard()
			ctx := context.Background()

			routeTableID, err := opts.CreatePublicRouteTable(
				ctx,
				logger,
				mockEC2,
				"vpc-123",
				"igw-123",
				[]string{"subnet-123", "subnet-456"},
			)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(routeTableID).NotTo(BeEmpty())
			}
		})
	}
}
