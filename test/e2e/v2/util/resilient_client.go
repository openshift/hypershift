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
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"
	utilnet "k8s.io/apimachinery/pkg/util/net"
)

const (
	defaultBaseDelay = 1 * time.Second
	defaultMaxDelay  = 30 * time.Second
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

// WithAuthRefreshOnRetry clears the Authorization header before each retry so
// an authentication wrapper around the resilient transport can refresh its
// credentials.
func WithAuthRefreshOnRetry() ResilientTransportOption {
	return func(rt *ResilientTransport) {
		rt.refreshAuthOnRetry = true
	}
}

// ResilientTransport wraps an http.RoundTripper and retries transport errors
// for requests that are safe to replay until the request context is canceled.
// HTTP responses, including 4xx/5xx status codes, are returned immediately.
// This is intended for management cluster API clients in CI, where an
// external job timeout is the stop condition.
//
// GET, HEAD, OPTIONS, and TRACE are safe to replay by definition. The
// management client also uses fixed-value JSON merge PATCHes; those are
// replayed only when they do not include metadata.resourceVersion. POST, PUT,
// DELETE, JSON Patch, and optimistic-lock PATCH requests are sent once and
// must handle an ambiguous transport error at the operation level.
//
// The request body must be nil or implement GetBody for retries to work;
// otherwise the first retry that needs to re-read the body will fail.
// Kubernetes client-go sets GetBody on all requests.
type ResilientTransport struct {
	delegate           http.RoundTripper
	baseDelay          time.Duration
	maxDelay           time.Duration
	refreshAuthOnRetry bool
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

// RoundTrip implements http.RoundTripper. It retries replayable requests on
// delegate errors using exponential backoff until the request context is
// done. Non-replayable requests are sent once. Successful responses (including
// HTTP error status codes) are returned immediately.
func (rt *ResilientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isReplayableRequest(req) {
		return rt.delegate.RoundTrip(req)
	}

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
			if rt.refreshAuthOnRetry {
				attemptReq.Header.Del("Authorization")
			}
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
	if attempt <= 0 || rt.baseDelay <= 0 || rt.maxDelay <= 0 {
		return 0
	}

	delay := rt.baseDelay
	if delay >= rt.maxDelay {
		return rt.maxDelay
	}

	for i := 1; i < attempt; i++ {
		// Cap before multiplying so a long-lived retry loop cannot overflow
		// time.Duration and turn into a negative or zero delay.
		if delay > rt.maxDelay-delay {
			return rt.maxDelay
		}
		delay += delay
	}

	return delay
}

func isReplayableRequest(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	case http.MethodPatch:
		return isReplayableMergePatch(req)
	default:
		return false
	}
}

func isReplayableMergePatch(req *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || mediaType != string(types.MergePatchType) {
		return false
	}
	if req.Body == nil {
		return true
	}
	if req.GetBody == nil {
		return false
	}

	body, err := req.GetBody()
	if err != nil {
		return false
	}
	defer body.Close()

	patch := map[string]json.RawMessage{}
	data, err := io.ReadAll(body)
	if err != nil || json.Unmarshal(data, &patch) != nil {
		return false
	}
	metadata, ok := patch["metadata"]
	if !ok {
		return true
	}
	metadataPatch := map[string]json.RawMessage{}
	if json.Unmarshal(metadata, &metadataPatch) != nil {
		return false
	}
	_, hasResourceVersion := metadataPatch["resourceVersion"]
	return !hasResourceVersion
}

// isTransportError reports whether an error came from the HTTP transport
// rather than from an HTTP response. It is used by operation-level write
// recovery, where repeating a fixed-name create or re-reading before an
// update is safe.
func isTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if utilnet.IsProbableEOF(err) || utilnet.IsTimeout(err) {
		return true
	}
	if strings.Contains(strings.ToLower(err.Error()), "http2: client connection lost") {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr)
}
