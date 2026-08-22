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
	"errors"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

const (
	defaultMaxRetries  = 5
	defaultBaseDelay   = 1 * time.Second
	defaultMaxDelay    = 30 * time.Second
	defaultRetryFactor = 2.0
)

// ResilientTransportOption configures a ResilientTransport.
type ResilientTransportOption func(*ResilientTransport)

// WithMaxRetries sets the maximum number of retry attempts.
func WithMaxRetries(n int) ResilientTransportOption {
	return func(rt *ResilientTransport) {
		rt.maxRetries = n
	}
}

// WithBaseDelay sets the initial backoff delay.
func WithBaseDelay(d time.Duration) ResilientTransportOption {
	return func(rt *ResilientTransport) {
		rt.baseDelay = d
	}
}

// WithMaxDelay sets the maximum backoff delay cap.
func WithMaxDelay(d time.Duration) ResilientTransportOption {
	return func(rt *ResilientTransport) {
		rt.maxDelay = d
	}
}

// ResilientTransport wraps an http.RoundTripper with retry logic for
// transient network errors such as connection refused, i/o timeout,
// connection reset by peer, and EOF. It uses exponential backoff between
// retry attempts. This is intended for management cluster API clients
// running in CI environments where Azure load balancer connectivity
// blips cause spurious failures.
type ResilientTransport struct {
	delegate   http.RoundTripper
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

// NewResilientTransport wraps delegate with retry logic for transient
// network errors. If delegate is nil, http.DefaultTransport is used.
func NewResilientTransport(delegate http.RoundTripper, opts ...ResilientTransportOption) *ResilientTransport {
	if delegate == nil {
		delegate = http.DefaultTransport
	}
	rt := &ResilientTransport{
		delegate:   delegate,
		maxRetries: defaultMaxRetries,
		baseDelay:  defaultBaseDelay,
		maxDelay:   defaultMaxDelay,
	}
	for _, opt := range opts {
		opt(rt)
	}
	if rt.maxRetries < 0 {
		rt.maxRetries = 0
	}
	return rt
}

// RoundTrip implements http.RoundTripper. It retries the request when
// the delegate returns a transient network error, using exponential
// backoff. Non-transient errors and successful responses (including
// HTTP error status codes) are returned immediately.
//
// The request body must be nil or implement GetBody for retries to work;
// otherwise the first retry that needs to re-read the body will fail.
// Kubernetes client-go sets GetBody on all requests.
func (rt *ResilientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= rt.maxRetries; attempt++ {
		attemptReq := req
		if attempt > 0 {
			// A body that cannot be rewound must not be replayed; the
			// delegate's first attempt already drained/closed it.
			if req.Body != nil && req.GetBody == nil {
				return nil, lastErr
			}

			delay := rt.backoffDelay(attempt)
			log.Printf("resilient transport: retrying %s %s (attempt %d/%d) after %v: %v",
				req.Method, req.URL.Path, attempt, rt.maxRetries, delay, lastErr)

			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(delay):
			}

			// Clone the request so the caller's original is never mutated,
			// then reset the clone's body for retry.
			attemptReq = req.Clone(req.Context())
			if req.Body != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, err
				}
				attemptReq.Body = body
			}
		}

		resp, err := rt.delegate.RoundTrip(attemptReq)
		if err == nil {
			return resp, nil
		}

		if !isTransientError(err) {
			return nil, err
		}
		lastErr = err
	}

	return nil, lastErr
}

// backoffDelay calculates exponential backoff with a cap.
func (rt *ResilientTransport) backoffDelay(attempt int) time.Duration {
	delay := time.Duration(float64(rt.baseDelay) * math.Pow(defaultRetryFactor, float64(attempt-1)))
	if delay > rt.maxDelay {
		delay = rt.maxDelay
	}
	return delay
}

// isTransientError returns true if the error is a transient network
// error that may resolve on retry. It checks for:
//   - syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ETIMEDOUT, syscall.EPIPE
//     (including when wrapped in a net.OpError)
//   - io.EOF and io.ErrUnexpectedEOF
//   - net.Error with Timeout() == true
//   - "connection reset by peer" in error message (catches wrapped errors)
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// io.EOF and io.ErrUnexpectedEOF indicate the connection was
	// closed before the response was fully read.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// syscall-level connection errors.
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	// net.Error with timeout flag covers i/o timeout, dial timeout, etc.
	// This also catches timeouts wrapped in net.OpError since it implements
	// net.Error.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Fallback: check the error string for known transient patterns.
	// This catches errors that may be wrapped in ways that defeat
	// type assertions.
	msg := err.Error()
	transientPatterns := []string{
		"connection reset by peer",
		"connection refused",
		"i/o timeout",
		"no such host",
		"TLS handshake timeout",
	}
	for _, pattern := range transientPatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}

	return false
}
