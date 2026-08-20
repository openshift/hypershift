package awsprivatelink

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"

	"go.uber.org/mock/gomock"
)

// testAPIError implements smithy.APIError for testing.
type testAPIError struct {
	code string
}

func (e *testAPIError) Error() string                 { return e.code }
func (e *testAPIError) ErrorCode() string             { return e.code }
func (e *testAPIError) ErrorMessage() string          { return e.code }
func (e *testAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

func TestFindRecord(t *testing.T) {
	const (
		recordName = "test.example.com"
		recordType = route53types.RRTypeA
	)

	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockROUTE53API)
		expectNil     bool
		expectError   bool
		errorContains string
	}{
		{
			name: "When record exists with matching name and type it should return the record set",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{
								Name: aws.String("test.example.com."),
								Type: route53types.RRTypeA,
							},
						},
					}, nil,
				)
			},
		},
		{
			name: "When no records are returned it should return nil without error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{},
					}, nil,
				)
			},
			expectNil: true,
		},
		{
			name: "When returned record name does not match it should return nil without error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{
								Name: aws.String("other.example.com."),
								Type: route53types.RRTypeA,
							},
						},
					}, nil,
				)
			},
			expectNil: true,
		},
		{
			name: "When returned record type does not match it should return nil without error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{
								Name: aws.String("test.example.com."),
								Type: route53types.RRTypeCname,
							},
						},
					}, nil,
				)
			},
			expectNil: true,
		},
		{
			name: "When API returns an error it should propagate the error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("api error"),
				)
			},
			expectNil:     true,
			expectError:   true,
			errorContains: "api error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			result, err := FindRecord(context.Background(), mockR53, "ZONE123", recordName, recordType)

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
			if tt.expectNil && result != nil {
				t.Errorf("expected nil result, got: %+v", result)
			}
			if !tt.expectNil && !tt.expectError && result == nil {
				t.Error("expected non-nil result, got nil")
			}
		})
	}
}

func TestCreateRecord(t *testing.T) {
	tests := []struct {
		name           string
		setupMock      func(*awsapi.MockROUTE53API)
		expectError    bool
		errorContains  string
		checkErrorType func(*testing.T, error)
	}{
		{
			name: "When ChangeResourceRecordSets succeeds it should return nil",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ChangeResourceRecordSetsOutput{}, nil,
				)
			},
			expectError: false,
		},
		{
			name: "When API returns a smithy error it should preserve the original error type",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &testAPIError{code: "NoSuchHostedZone"},
				)
			},
			expectError:   true,
			errorContains: "NoSuchHostedZone",
			checkErrorType: func(t *testing.T, err error) {
				var apiErr smithy.APIError
				if !errors.As(err, &apiErr) {
					t.Error("expected error to implement smithy.APIError for typed error detection")
				}
			},
		},
		{
			name: "When API returns a non-smithy error it should propagate the original error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("network error"),
				)
			},
			expectError:   true,
			errorContains: "network error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			err := CreateRecord(context.Background(), mockR53, "ZONE123", "test.example.com", "10.0.0.1", route53types.RRTypeA)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got: %v", tt.errorContains, err)
				}
				if tt.checkErrorType != nil {
					tt.checkErrorType(t, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}
		})
	}
}

func TestIsAWSNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error code is NoSuchHostedZone it should return true",
			err:      &testAPIError{code: "NoSuchHostedZone"},
			expected: true,
		},
		{
			name:     "When error code is HostedZoneNotFound it should return true",
			err:      &testAPIError{code: "HostedZoneNotFound"},
			expected: true,
		},
		{
			name:     "When error code is something else it should return false",
			err:      &testAPIError{code: "Throttling"},
			expected: false,
		},
		{
			name:     "When error is not an AWS API error it should return false",
			err:      errors.New("network error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAWSNotFoundError(tt.err); got != tt.expected {
				t.Errorf("isAWSNotFoundError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsAWSConflictError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error code is HostedZoneAlreadyExists it should return true",
			err:      &testAPIError{code: "HostedZoneAlreadyExists"},
			expected: true,
		},
		{
			name:     "When error code is ConflictingDomainExists it should return true",
			err:      &testAPIError{code: "ConflictingDomainExists"},
			expected: true,
		},
		{
			name:     "When error code is something else it should return false",
			err:      &testAPIError{code: "NoSuchHostedZone"},
			expected: false,
		},
		{
			name:     "When error is not an AWS API error it should return false",
			err:      errors.New("network error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAWSConflictError(tt.err); got != tt.expected {
				t.Errorf("isAWSConflictError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCreatePrivateHostedZone(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockROUTE53API)
		expectZoneID  string
		expectError   bool
		errorContains string
	}{
		{
			name: "When zone already exists via lookup it should return existing zone ID",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesOutput{
						HostedZones: []route53types.HostedZone{{
							Id:     aws.String("/hostedzone/ZEXISTING"),
							Name:   aws.String("test.hypershift.local."),
							Config: &route53types.HostedZoneConfig{PrivateZone: true},
						}},
					}, nil,
				)
			},
			expectZoneID: "ZEXISTING",
		},
		{
			name: "When zone does not exist it should create and return new zone ID",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesOutput{HostedZones: []route53types.HostedZone{}}, nil,
				)
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.CreateHostedZoneOutput{
						HostedZone: &route53types.HostedZone{Id: aws.String("/hostedzone/ZNEW")},
					}, nil,
				)
			},
			expectZoneID: "ZNEW",
		},
		{
			name: "When create returns HostedZoneAlreadyExists it should lookup and return existing",
			setupMock: func(m *awsapi.MockROUTE53API) {
				// First lookup misses
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesOutput{HostedZones: []route53types.HostedZone{}}, nil,
				)
				// Create gets conflict
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &testAPIError{code: "HostedZoneAlreadyExists"},
				)
				// Conflict lookup finds it
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesOutput{
						HostedZones: []route53types.HostedZone{{
							Id:     aws.String("/hostedzone/ZCONFLICT"),
							Name:   aws.String("test.hypershift.local."),
							Config: &route53types.HostedZoneConfig{PrivateZone: true},
						}},
					}, nil,
				)
			},
			expectZoneID: "ZCONFLICT",
		},
		{
			name: "When create fails with non-conflict error it should return error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesOutput{HostedZones: []route53types.HostedZone{}}, nil,
				)
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("access denied"),
				)
			},
			expectError:   true,
			errorContains: "failed to create private hosted zone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			ctx := context.Background()
			zoneID, err := CreatePrivateHostedZone(ctx, mockR53, "test.hypershift.local", "vpc-123", "us-east-1")

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
				if zoneID != tt.expectZoneID {
					t.Errorf("expected zone ID %q, got %q", tt.expectZoneID, zoneID)
				}
			}
		})
	}
}

func TestCreatePublicHostedZone(t *testing.T) {
	tests := []struct {
		name             string
		setupMock        func(*awsapi.MockROUTE53API)
		expectZoneID     string
		expectNSCount    int
		expectError      bool
		errorContains    string
	}{
		{
			name: "When zone does not exist it should create and return zone ID and name servers",
			setupMock: func(m *awsapi.MockROUTE53API) {
				// lookupPublicZoneID finds nothing
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesOutput{HostedZones: []route53types.HostedZone{}}, nil,
				)
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.CreateHostedZoneOutput{
						HostedZone:    &route53types.HostedZone{Id: aws.String("/hostedzone/ZPUB")},
						DelegationSet: &route53types.DelegationSet{NameServers: []string{"ns-1.awsdns-01.com", "ns-2.awsdns-02.net"}},
					}, nil,
				)
			},
			expectZoneID:  "ZPUB",
			expectNSCount: 2,
		},
		{
			name: "When zone already exists it should return existing zone and name servers",
			setupMock: func(m *awsapi.MockROUTE53API) {
				// lookupPublicZoneID finds it
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesOutput{
						HostedZones: []route53types.HostedZone{{
							Id:   aws.String("/hostedzone/ZEXISTING"),
							Name: aws.String("in.test.example.com."),
						}},
					}, nil,
				)
				// GetHostedZone for name servers
				m.EXPECT().GetHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.GetHostedZoneOutput{
						DelegationSet: &route53types.DelegationSet{NameServers: []string{"ns-1.awsdns-01.com"}},
					}, nil,
				)
			},
			expectZoneID:  "ZEXISTING",
			expectNSCount: 1,
		},
		{
			name: "When create fails with non-conflict error it should return error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListHostedZonesOutput{HostedZones: []route53types.HostedZone{}}, nil,
				)
				m.EXPECT().CreateHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("quota exceeded"),
				)
			},
			expectError:   true,
			errorContains: "failed to create public hosted zone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			ctx := context.Background()
			zoneID, nameServers, err := CreatePublicHostedZone(ctx, mockR53, "in.test.example.com")

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
				if zoneID != tt.expectZoneID {
					t.Errorf("expected zone ID %q, got %q", tt.expectZoneID, zoneID)
				}
				if len(nameServers) != tt.expectNSCount {
					t.Errorf("expected %d name servers, got %d", tt.expectNSCount, len(nameServers))
				}
			}
		})
	}
}

func TestDeleteHostedZoneWithRecords(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockROUTE53API)
		expectError   bool
		errorContains string
	}{
		{
			name: "When zone has custom records it should delete records then zone",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{Name: aws.String("example.com."), Type: route53types.RRTypeSoa},
							{Name: aws.String("example.com."), Type: route53types.RRTypeNs},
							{Name: aws.String("_acme-challenge.example.com."), Type: route53types.RRTypeCname, TTL: aws.Int64(300), ResourceRecords: []route53types.ResourceRecord{{Value: aws.String("target.com")}}},
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
			name: "When zone has no custom records it should skip drain and delete zone",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{Name: aws.String("example.com."), Type: route53types.RRTypeSoa},
							{Name: aws.String("example.com."), Type: route53types.RRTypeNs},
						},
					}, nil,
				)
				m.EXPECT().DeleteHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.DeleteHostedZoneOutput{}, nil,
				)
			},
		},
		{
			name: "When zone is already deleted it should return nil",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &testAPIError{code: "NoSuchHostedZone"},
				)
				m.EXPECT().DeleteHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &testAPIError{code: "NoSuchHostedZone"},
				)
			},
		},
		{
			name: "When delete zone fails it should return error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{
							{Name: aws.String("example.com."), Type: route53types.RRTypeSoa},
						},
					}, nil,
				)
				m.EXPECT().DeleteHostedZone(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("access denied"),
				)
			},
			expectError:   true,
			errorContains: "failed to delete hosted zone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			ctx := context.Background()
			err := DeleteHostedZoneWithRecords(ctx, mockR53, "ZTEST")

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

func TestDeleteRecord(t *testing.T) {
	record := &route53types.ResourceRecordSet{
		Name: aws.String("test.example.com."),
		Type: route53types.RRTypeA,
		TTL:  aws.Int64(300),
	}

	tests := []struct {
		name          string
		setupMock     func(*awsapi.MockROUTE53API)
		expectError   bool
		errorContains string
	}{
		{
			name: "When ChangeResourceRecordSets succeeds it should return nil",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&route53.ChangeResourceRecordSetsOutput{}, nil,
				)
			},
			expectError: false,
		},
		{
			name: "When ChangeResourceRecordSets fails it should propagate the error",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, errors.New("delete failed"),
				)
			},
			expectError:   true,
			errorContains: "delete failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockR53 := awsapi.NewMockROUTE53API(ctrl)
			tt.setupMock(mockR53)

			err := DeleteRecord(context.Background(), mockR53, "ZONE123", record)

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
