package azureutil

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestParseRetryAfterDuration(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedOK     bool
		expectedMinDur time.Duration
		expectedMaxDur time.Duration
	}{
		{
			name:       "When error is nil it should return false",
			err:        nil,
			expectedOK: false,
		},
		{
			name:       "When error is not an Azure error it should return false",
			err:        fmt.Errorf("some random error"),
			expectedOK: false,
		},
		{
			name: "When RawResponse is nil it should return false",
			err: &azcore.ResponseError{
				StatusCode:  429,
				RawResponse: nil,
			},
			expectedOK: false,
		},
		{
			name: "When Retry-After header is absent it should return false",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{},
				},
			},
			expectedOK: false,
		},
		{
			name: "When Retry-After header is empty it should return false",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {""},
					},
				},
			},
			expectedOK: false,
		},
		{
			name: "When Retry-After is seconds it should parse correctly",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {"120"},
					},
				},
			},
			expectedOK:     true,
			expectedMinDur: 120 * time.Second,
			expectedMaxDur: 120 * time.Second,
		},
		{
			name: "When Retry-After is 30 seconds it should parse correctly",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {"30"},
					},
				},
			},
			expectedOK:     true,
			expectedMinDur: 30 * time.Second,
			expectedMaxDur: 30 * time.Second,
		},
		{
			name: "When Retry-After is zero seconds it should return false",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {"0"},
					},
				},
			},
			expectedOK: false,
		},
		{
			name: "When Retry-After is negative seconds it should return false",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {"-5"},
					},
				},
			},
			expectedOK: false,
		},
		{
			name: "When Retry-After is a future HTTP-date it should return positive duration",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)},
					},
				},
			},
			expectedOK:     true,
			expectedMinDur: 1 * time.Minute, // Allow some tolerance
			expectedMaxDur: 3 * time.Minute, // Allow some tolerance
		},
		{
			name: "When Retry-After is a past HTTP-date it should return false",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {time.Now().Add(-5 * time.Minute).UTC().Format(http.TimeFormat)},
					},
				},
			},
			expectedOK: false,
		},
		{
			name: "When Retry-After is an unparsable string it should return false",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {"not-a-number-or-date"},
					},
				},
			},
			expectedOK: false,
		},
		{
			name: "When error wraps an Azure ResponseError it should still parse",
			err: fmt.Errorf("outer error: %w", &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {"60"},
					},
				},
			}),
			expectedOK:     true,
			expectedMinDur: 60 * time.Second,
			expectedMaxDur: 60 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, ok := ParseRetryAfterDuration(tt.err)
			if ok != tt.expectedOK {
				t.Errorf("expected ok=%v, got ok=%v (duration=%v)", tt.expectedOK, ok, d)
			}
			if tt.expectedOK {
				if d < tt.expectedMinDur || d > tt.expectedMaxDur {
					t.Errorf("expected duration in [%v, %v], got %v", tt.expectedMinDur, tt.expectedMaxDur, d)
				}
			}
		})
	}
}

func TestClassifyAzureError(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		expectedRequeue time.Duration
		expectedMessage string
	}{
		{
			name: "When Azure returns 429 without Retry-After it should use default 5 minutes",
			err: &azcore.ResponseError{
				StatusCode:  429,
				RawResponse: &http.Response{Header: http.Header{}},
			},
			expectedRequeue: 5 * time.Minute,
			expectedMessage: "Azure API rate limit exceeded, retrying",
		},
		{
			name: "When Azure returns 429 with Retry-After seconds it should use that duration",
			err: &azcore.ResponseError{
				StatusCode: 429,
				RawResponse: &http.Response{
					Header: http.Header{
						"Retry-After": {"120"},
					},
				},
			},
			expectedRequeue: 120 * time.Second,
			expectedMessage: "Azure API rate limit exceeded, retrying",
		},
		{
			name: "When Azure returns 429 with nil RawResponse it should use default 5 minutes",
			err: &azcore.ResponseError{
				StatusCode:  429,
				RawResponse: nil,
			},
			expectedRequeue: 5 * time.Minute,
			expectedMessage: "Azure API rate limit exceeded, retrying",
		},
		{
			name:            "When Azure returns 403 it should requeue after 10 minutes",
			err:             &azcore.ResponseError{StatusCode: 403},
			expectedRequeue: 10 * time.Minute,
			expectedMessage: "Azure API permission denied, check service principal permissions",
		},
		{
			name:            "When Azure returns 409 it should requeue after 30 seconds",
			err:             &azcore.ResponseError{StatusCode: 409},
			expectedRequeue: 30 * time.Second,
			expectedMessage: "Azure resource conflict, retrying",
		},
		{
			name: "When Azure returns 500 it should requeue after 2 minutes",
			err: &azcore.ResponseError{
				StatusCode: 500,
				ErrorCode:  "InternalServerError",
			},
			expectedRequeue: 2 * time.Minute,
			expectedMessage: "Azure API error (HTTP 500): InternalServerError",
		},
		{
			name:            "When error is not Azure it should requeue after 2 minutes",
			err:             errors.New("network timeout"),
			expectedRequeue: 2 * time.Minute,
			expectedMessage: "Unexpected error: network timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requeue, message := ClassifyAzureError(tt.err)
			if requeue != tt.expectedRequeue {
				t.Errorf("expected requeue %v, got %v", tt.expectedRequeue, requeue)
			}
			if message != tt.expectedMessage {
				t.Errorf("expected message %q, got %q", tt.expectedMessage, message)
			}
		})
	}
}
