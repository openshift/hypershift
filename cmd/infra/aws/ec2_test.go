package aws

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/aws/smithy-go"
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
			name:     "When error is invalidRouteTableID it should be retriable",
			err:      &testAPIError{code: invalidRouteTableID},
			expected: true,
		},
		{
			name:     "When error is RequestLimitExceeded it should be retriable",
			err:      &testAPIError{code: "RequestLimitExceeded"},
			expected: true,
		},
		{
			name:     "When error is Throttling it should be retriable",
			err:      &testAPIError{code: "Throttling"},
			expected: true,
		},
		{
			name:     "When error is EC2ThrottledException it should be retriable",
			err:      &testAPIError{code: "EC2ThrottledException"},
			expected: true,
		},
		{
			name:     "When error is a non-retriable API error it should not be retriable",
			err:      &testAPIError{code: "InvalidParameterValue"},
			expected: false,
		},
		{
			name:     "When error is not an API error it should not be retriable",
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
