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
	"net/http"
	"sync/atomic"
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

func TestResilientTransport(t *testing.T) {
	t.Run("When the first request succeeds, it should not retry", func(t *testing.T) {
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
	})

	t.Run("When the request fails then succeeds, it should retry until success", func(t *testing.T) {
		delegate := &fakeRoundTripper{
			responses: []roundTripResult{
				{err: io.EOF},
				{err: errors.New("certificate has expired")},
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
	})

	t.Run("When the HTTP status is an error, it should return the response without retrying", func(t *testing.T) {
		delegate := &fakeRoundTripper{
			responses: []roundTripResult{
				{resp: &http.Response{StatusCode: http.StatusInternalServerError}},
			},
		}
		rt := NewResilientTransport(delegate)
		resp, err := rt.RoundTrip(newRequest(t))
		if err != nil {
			t.Fatalf("expected no transport error, got: %v", err)
		}
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected status 500, got %d", resp.StatusCode)
		}
		if delegate.calls.Load() != 1 {
			t.Fatalf("expected 1 call, got %d", delegate.calls.Load())
		}
	})

	t.Run("When the context is canceled during backoff, it should return the context error", func(t *testing.T) {
		delegate := &fakeRoundTripper{
			responses: []roundTripResult{
				{err: io.EOF},
				{err: io.EOF},
			},
		}
		rt := NewResilientTransport(delegate, WithBaseDelay(5*time.Second))

		ctx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.com/healthz", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}

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
	})

	t.Run("When backoff grows, it should cap the delay at maxDelay", func(t *testing.T) {
		rt := NewResilientTransport(nil, WithMaxDelay(100*time.Millisecond), WithBaseDelay(10*time.Millisecond))
		delay := rt.backoffDelay(10)
		if delay > 100*time.Millisecond {
			t.Fatalf("expected delay capped at 100ms, got %v", delay)
		}
	})

	t.Run("When delegate is nil, it should use the default transport", func(t *testing.T) {
		rt := NewResilientTransport(nil)
		if rt.delegate == nil {
			t.Fatal("expected default transport when delegate is nil")
		}
	})
}

func TestResilientTransport_ExponentialBackoff(t *testing.T) {
	t.Run("When retries are needed, it should wait with exponential backoff", func(t *testing.T) {
		baseDelay := 50 * time.Millisecond
		delegate := &fakeRoundTripper{
			responses: []roundTripResult{
				{err: io.EOF},
				{err: io.EOF},
				{err: io.EOF},
				{resp: &http.Response{StatusCode: http.StatusOK}},
			},
		}
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
		minExpected := 300 * time.Millisecond
		if elapsed < minExpected {
			t.Fatalf("expected at least %v of backoff delay, got %v", minExpected, elapsed)
		}
	})
}
