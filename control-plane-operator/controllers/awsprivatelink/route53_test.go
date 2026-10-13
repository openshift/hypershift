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
			name: "When API returns a smithy error it should return the error code as the error message",
			setupMock: func(m *awsapi.MockROUTE53API) {
				m.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					nil, &testAPIError{code: "NoSuchHostedZone"},
				)
			},
			expectError:   true,
			errorContains: "NoSuchHostedZone",
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
