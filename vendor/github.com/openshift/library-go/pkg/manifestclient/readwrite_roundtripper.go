package manifestclient

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"k8s.io/client-go/rest"
	"net/http"
	"os"
)

// RecommendedRESTConfig is meant to be paired with the HTTPClients below
func RecommendedRESTConfig() *rest.Config {
	return &rest.Config{
		QPS:   1000,
		Burst: 10000,
	}
}

// Enter here and call `NewForConfigAndClient(manifestclient.RecommendedRESTConfig(), httpClient)`
func NewHTTPClient(mustGatherDir string) MutationTrackingClient {
	mutationTrackingRoundTripper := newReadWriteRoundTripper(os.DirFS(mustGatherDir))
	return &mutationTrackingClient{
		httpClient: &http.Client{
			Transport: mutationTrackingRoundTripper,
		},
		mutationTrackingRoundTripper: mutationTrackingRoundTripper,
	}
}

// Enter here and call `NewForConfigAndClient(manifestclient.RecommendedRESTConfig(), httpClient)`
func NewTestingHTTPClient(embedFS fs.FS) MutationTrackingClient {
	mutationTrackingRoundTripper := newReadWriteRoundTripper(embedFS)
	return &mutationTrackingClient{
		httpClient: &http.Client{
			Transport: mutationTrackingRoundTripper,
		},
		mutationTrackingRoundTripper: mutationTrackingRoundTripper,
	}
}

func NewTestingRoundTripper(embedFS fs.FS) *readWriteRoundTripper {
	return newReadWriteRoundTripper(embedFS)
}

func NewRoundTripper(mustGatherDir string) *readWriteRoundTripper {
	return newReadWriteRoundTripper(os.DirFS(mustGatherDir))
}

func newReadWriteRoundTripper(sourceFS fs.FS) *readWriteRoundTripper {
	rt := &readWriteRoundTripper{}
	discoveryReader := newDiscoveryReader(sourceFS)
	rt.readDelegate = newReadRoundTripper(sourceFS, discoveryReader)
	rt.writeDelegate = newWriteRoundTripper(discoveryReader)
	return rt
}

type readWriteRoundTripper struct {
	readDelegate  *manifestRoundTripper
	writeDelegate *writeTrackingRoundTripper
}

type MutationTrackingRoundTripper interface {
	http.RoundTripper
	GetMutations() *AllActionsTracker[TrackedSerializedRequest]
}

type mutationTrackingClient struct {
	httpClient *http.Client

	mutationTrackingRoundTripper MutationTrackingRoundTripper
}

func (m mutationTrackingClient) GetHTTPClient() *http.Client {
	return m.httpClient
}

func (m mutationTrackingClient) GetMutations() *AllActionsTracker[TrackedSerializedRequest] {
	return m.mutationTrackingRoundTripper.GetMutations()
}

type MutationTrackingClient interface {
	GetHTTPClient() *http.Client
	GetMutations() *AllActionsTracker[TrackedSerializedRequest]
}

func (rt *readWriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case "GET", "HEAD":
		return rt.readDelegate.RoundTrip(req)
	case "POST", "PUT", "PATCH", "DELETE":
		return rt.writeDelegate.RoundTrip(req)
	default:
		resp := &http.Response{}
		resp.StatusCode = http.StatusInternalServerError
		resp.Status = http.StatusText(resp.StatusCode)
		resp.Body = io.NopCloser(bytes.NewBufferString(fmt.Sprintf("unhandled verb: %q", req.Method)))
		return resp, nil
	}
}

func (rt *readWriteRoundTripper) GetMutations() *AllActionsTracker[TrackedSerializedRequest] {
	return rt.writeDelegate.GetMutations()
}
