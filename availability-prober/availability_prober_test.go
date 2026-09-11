package availabilityprober

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

type trackedBody struct {
	io.Reader
	read, closed int
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *trackedBody) Close() error {
	b.closed++
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type discoveryFunc struct {
	discovery.DiscoveryInterface
	resources func() ([]*metav1.APIGroup, []*metav1.APIResourceList, error)
}

func (d discoveryFunc) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return d.resources()
}

func TestCheck(t *testing.T) {
	for _, tt := range []struct {
		name                                                      string
		status                                                    int
		required, discoveryError, cancelDuringSleep, requestError bool
		bodySize                                                  int
	}{
		{name: "When the target is ready, it should close its body before returning", status: 200, bodySize: 2},
		{name: "When the status is not ready, it should close every body before retrying", status: 503, bodySize: 2},
		{name: "When a required API is missing, it should close every body before discovery and the next request", status: 200, required: true, bodySize: 2},
		{name: "When discovery fails, it should close every body before retrying", status: 200, required: true, discoveryError: true, bodySize: 2},
		{name: "When a response exceeds the drain bound, it should close without reading the entire body", status: 503, bodySize: 1 << 20},
		{name: "When cancellation interrupts a long retry sleep, it should return promptly", status: 503, cancelDuringSleep: true, bodySize: 2},
		{name: "When transport fails until cancellation, it should stop retrying", requestError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var bodies []*trackedBody
			attempts, discoveries := 0, 0
			assertClosed := func() {
				for i, body := range bodies {
					if body.closed != 1 || body.read != min(tt.bodySize, 4<<10) {
						t.Errorf("body %d: close count=%d bytes read=%d", i, body.closed, body.read)
					}
				}
			}
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				assertClosed()
				attempts++
				if attempts == 4 {
					cancel()
				}
				if tt.requestError {
					return nil, errors.New("target unavailable")
				}
				body := &trackedBody{Reader: strings.NewReader(strings.Repeat("x", tt.bodySize))}
				bodies = append(bodies, body)
				if tt.cancelDuringSleep {
					time.AfterFunc(20*time.Millisecond, cancel)
				}
				return &http.Response{StatusCode: tt.status, Header: http.Header{}, Body: body, Request: req}, nil
			})}
			d := discoveryFunc{resources: func() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
				assertClosed()
				discoveries++
				if tt.discoveryError {
					return nil, nil, errors.New("discovery unavailable")
				}
				return nil, nil, nil // Required API never appears.
			}}
			var required []schema.GroupVersionKind
			if tt.required {
				required = []schema.GroupVersionKind{{Group: "route.openshift.io", Version: "v1", Kind: "Route"}}
			}
			sleep := time.Millisecond
			if tt.cancelDuringSleep {
				sleep = time.Hour
			}
			target, _ := url.Parse("https://api.example.test/readyz")
			start := time.Now()
			check(ctx, logr.Discard(), target, client, sleep, required, false, "", "", d, nil)
			assertClosed()
			wantAttempts := 4
			if tt.cancelDuringSleep || (tt.status == 200 && !tt.required) {
				wantAttempts = 1
			}
			if attempts != wantAttempts {
				t.Fatalf("got %d attempts, want %d", attempts, wantAttempts)
			}
			if tt.required && discoveries != attempts {
				t.Fatalf("got %d discoveries for %d requests", discoveries, attempts)
			}
			if time.Since(start) > time.Second {
				t.Fatal("cancellation did not stop retry sleep promptly")
			}
		})
	}
	t.Run("When context is already cancelled, it should not issue a request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		target, _ := url.Parse("https://api.example.test/readyz")
		check(ctx, logr.Discard(), target, nil, time.Hour, nil, false, "", "", nil, nil)
	})
}
