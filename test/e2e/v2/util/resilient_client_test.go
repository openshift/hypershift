//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// fakeRoundTripper is a test double that returns configurable responses.
type fakeRoundTripper struct {
	responses []roundTripResult
	calls     atomic.Int32
}

type roundTripResult struct {
	resp *http.Response
	err  error
}

func (f *fakeRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	idx := int(f.calls.Add(1)) - 1
	if idx >= len(f.responses) {
		return nil, fmt.Errorf("unexpected call %d (only %d responses configured)", idx+1, len(f.responses))
	}
	return f.responses[idx].resp, f.responses[idx].err
}

func newRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/healthz", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	return req
}

func TestResilientTransport_SuccessNoRetry(t *testing.T) {
	delegate := &fakeRoundTripper{
		responses: []roundTripResult{
			{resp: &http.Response{StatusCode: http.StatusOK}, err: nil},
		},
	}
	rt := NewResilientTransport(delegate)
	resp, err := rt.RoundTrip(newRequest(t))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if delegate.calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", delegate.calls.Load())
	}
}

func TestResilientTransport_RetryOnConnectionRefused(t *testing.T) {
	connRefused := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &net.AddrError{Err: syscall.ECONNREFUSED.Error()},
	}
	delegate := &fakeRoundTripper{
		responses: []roundTripResult{
			{err: connRefused},
			{err: connRefused},
			{resp: &http.Response{StatusCode: http.StatusOK}},
		},
	}
	rt := NewResilientTransport(delegate, WithBaseDelay(1*time.Millisecond), WithMaxDelay(10*time.Millisecond))
	resp, err := rt.RoundTrip(newRequest(t))
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if delegate.calls.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", delegate.calls.Load())
	}
}

func TestResilientTransport_RetryOnEOF(t *testing.T) {
	delegate := &fakeRoundTripper{
		responses: []roundTripResult{
			{err: io.EOF},
			{resp: &http.Response{StatusCode: http.StatusOK}},
		},
	}
	rt := NewResilientTransport(delegate, WithBaseDelay(1*time.Millisecond))
	resp, err := rt.RoundTrip(newRequest(t))
	if err != nil {
		t.Fatalf("expected success after EOF retry, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if delegate.calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", delegate.calls.Load())
	}
}

func TestResilientTransport_RetryOnConnectionReset(t *testing.T) {
	delegate := &fakeRoundTripper{
		responses: []roundTripResult{
			{err: fmt.Errorf("read: connection reset by peer")},
			{resp: &http.Response{StatusCode: http.StatusOK}},
		},
	}
	rt := NewResilientTransport(delegate, WithBaseDelay(1*time.Millisecond))
	resp, err := rt.RoundTrip(newRequest(t))
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestResilientTransport_NoRetryOnNonTransientError(t *testing.T) {
	nonTransient := errors.New("certificate has expired")
	delegate := &fakeRoundTripper{
		responses: []roundTripResult{
			{err: nonTransient},
		},
	}
	rt := NewResilientTransport(delegate, WithBaseDelay(1*time.Millisecond))
	_, err := rt.RoundTrip(newRequest(t))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != nonTransient.Error() {
		t.Fatalf("expected error %q, got %q", nonTransient, err)
	}
	if delegate.calls.Load() != 1 {
		t.Fatalf("expected 1 call (no retry), got %d", delegate.calls.Load())
	}
}

func TestResilientTransport_RespectsMaxRetries(t *testing.T) {
	maxRetries := 3
	numResponses := maxRetries + 1 // initial attempt + retries
	responses := make([]roundTripResult, numResponses)
	for i := range responses {
		responses[i] = roundTripResult{err: io.EOF}
	}
	delegate := &fakeRoundTripper{responses: responses}
	rt := NewResilientTransport(delegate,
		WithMaxRetries(maxRetries),
		WithBaseDelay(1*time.Millisecond),
	)

	_, err := rt.RoundTrip(newRequest(t))
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got: %v", err)
	}
	if delegate.calls.Load() != int32(numResponses) {
		t.Fatalf("expected %d calls, got %d", numResponses, delegate.calls.Load())
	}
}

func TestResilientTransport_ExponentialBackoff(t *testing.T) {
	baseDelay := 50 * time.Millisecond
	responses := []roundTripResult{
		{err: io.EOF},
		{err: io.EOF},
		{err: io.EOF},
		{resp: &http.Response{StatusCode: http.StatusOK}},
	}
	delegate := &fakeRoundTripper{responses: responses}
	rt := NewResilientTransport(delegate,
		WithBaseDelay(baseDelay),
		WithMaxDelay(1*time.Second),
	)

	start := time.Now()
	resp, err := rt.RoundTrip(newRequest(t))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// With base=50ms and factor=2: delays are 50ms, 100ms, 200ms = 350ms total minimum.
	// Allow some slack for test execution overhead.
	minExpected := 300 * time.Millisecond // slightly less than 350ms to account for timing
	if elapsed < minExpected {
		t.Fatalf("expected at least %v of backoff delay, got %v", minExpected, elapsed)
	}
}

func TestResilientTransport_ContextCancellation(t *testing.T) {
	delegate := &fakeRoundTripper{
		responses: []roundTripResult{
			{err: io.EOF},
			{err: io.EOF}, // should not reach this
		},
	}
	rt := NewResilientTransport(delegate,
		WithBaseDelay(5*time.Second), // long delay so context cancels first
	)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.com/healthz", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Cancel the context shortly after the first attempt fails.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err = rt.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestResilientTransport_NilDelegate(t *testing.T) {
	rt := NewResilientTransport(nil)
	if rt.delegate == nil {
		t.Fatal("expected default transport when delegate is nil")
	}
}

func TestResilientTransport_BackoffDelayCap(t *testing.T) {
	rt := NewResilientTransport(nil, WithMaxDelay(100*time.Millisecond), WithBaseDelay(10*time.Millisecond))
	// attempt 10 would give 10ms * 2^9 = 5120ms without cap
	delay := rt.backoffDelay(10)
	if delay > 100*time.Millisecond {
		t.Fatalf("expected delay capped at 100ms, got %v", delay)
	}
}

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "io.EOF",
			err:      io.EOF,
			expected: true,
		},
		{
			name:     "io.ErrUnexpectedEOF",
			err:      io.ErrUnexpectedEOF,
			expected: true,
		},
		{
			name:     "wrapped io.EOF",
			err:      fmt.Errorf("read failed: %w", io.EOF),
			expected: true,
		},
		{
			name:     "ECONNREFUSED",
			err:      syscall.ECONNREFUSED,
			expected: true,
		},
		{
			name:     "ECONNRESET",
			err:      syscall.ECONNRESET,
			expected: true,
		},
		{
			name:     "ETIMEDOUT",
			err:      syscall.ETIMEDOUT,
			expected: true,
		},
		{
			name:     "EPIPE",
			err:      syscall.EPIPE,
			expected: true,
		},
		{
			name: "net.OpError",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: fmt.Errorf("connection refused"),
			},
			expected: true,
		},
		{
			name:     "connection reset by peer string",
			err:      fmt.Errorf("read tcp 10.0.0.1:443: connection reset by peer"),
			expected: true,
		},
		{
			name:     "i/o timeout string",
			err:      fmt.Errorf("dial tcp: i/o timeout"),
			expected: true,
		},
		{
			name:     "TLS handshake timeout",
			err:      fmt.Errorf("net/http: TLS handshake timeout"),
			expected: true,
		},
		{
			name:     "non-transient error",
			err:      errors.New("certificate has expired"),
			expected: false,
		},
		{
			name:     "non-transient wrapped error",
			err:      fmt.Errorf("tls: %w", errors.New("bad certificate")),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientError(tt.err)
			if got != tt.expected {
				t.Errorf("isTransientError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}
