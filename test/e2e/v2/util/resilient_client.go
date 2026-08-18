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
	"log"
	"math"
	"net/http"
	"time"
)

const (
	defaultBaseDelay   = 1 * time.Second
	defaultMaxDelay    = 30 * time.Second
	defaultRetryFactor = 2.0
)

// ResilientTransportOption configures a ResilientTransport.
type ResilientTransportOption func(*ResilientTransport)

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

// ResilientTransport wraps an http.RoundTripper and retries any transport
// error with exponential backoff until the request context is canceled.
// HTTP responses, including 4xx/5xx status codes, are returned immediately.
// This is intended for management cluster API clients in CI, where an
// external job timeout is the stop condition.
//
// The request body must be nil or implement GetBody for retries to work;
// otherwise the first retry that needs to re-read the body will fail.
// Kubernetes client-go sets GetBody on all requests.
type ResilientTransport struct {
	delegate  http.RoundTripper
	baseDelay time.Duration
	maxDelay  time.Duration
}

// NewResilientTransport wraps delegate with retry-until-canceled logic.
// If delegate is nil, http.DefaultTransport is used.
func NewResilientTransport(delegate http.RoundTripper, opts ...ResilientTransportOption) *ResilientTransport {
	if delegate == nil {
		delegate = http.DefaultTransport
	}
	rt := &ResilientTransport{
		delegate:  delegate,
		baseDelay: defaultBaseDelay,
		maxDelay:  defaultMaxDelay,
	}
	for _, opt := range opts {
		opt(rt)
	}
	return rt
}

// RoundTrip implements http.RoundTripper. It retries the request on any
// delegate error using exponential backoff until the request context is
// done. Successful responses (including HTTP error status codes) are
// returned immediately.
func (rt *ResilientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		attemptReq := req
		if attempt > 0 {
			// A body that cannot be rewound must not be replayed; the
			// delegate's first attempt already drained/closed it.
			if req.Body != nil && req.GetBody == nil {
				return nil, lastErr
			}

			delay := rt.backoffDelay(attempt)
			log.Printf("resilient transport: retrying %s %s (attempt %d) after %v: %v",
				req.Method, req.URL.Path, attempt, delay, lastErr)

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
		lastErr = err
	}
}

// backoffDelay calculates exponential backoff with a cap.
func (rt *ResilientTransport) backoffDelay(attempt int) time.Duration {
	delay := time.Duration(float64(rt.baseDelay) * math.Pow(defaultRetryFactor, float64(attempt-1)))
	if delay > rt.maxDelay {
		delay = rt.maxDelay
	}
	return delay
}
