package gcpprivateserviceconnect

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/option"
)

// zoneRoundTripper adapts a function to http.RoundTripper so a dns.Service can be backed
// by an in-memory fake instead of real GCP credentials.
type zoneRoundTripper func(*http.Request) (*http.Response, error)

func (f zoneRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeZoneServer is an in-memory stand-in for the Cloud DNS managed-zone API. It serves
// the Get, Create and Patch calls that zone reconciliation makes and records the
// mutations so tests can assert on what was sent.
type fakeZoneServer struct {
	// zones are the zones that already exist, keyed by zone name.
	zones map[string]*dns.ManagedZone
	// created records the zones passed to Create, in call order.
	created []*dns.ManagedZone
	// patched records the label maps passed to Patch, keyed by zone name.
	patched map[string]map[string]string
	// getErrorCode, when non-zero, makes Get fail with that HTTP status.
	getErrorCode int
}

func newFakeZoneServer(existing ...*dns.ManagedZone) *fakeZoneServer {
	zones := map[string]*dns.ManagedZone{}
	for _, zone := range existing {
		zones[zone.Name] = zone
	}
	return &fakeZoneServer{zones: zones, patched: map[string]map[string]string{}}
}

// service returns a dns.Service wired to the fake.
func (s *fakeZoneServer) service(t *testing.T) *dns.Service {
	t.Helper()

	jsonResp := func(r *http.Request, code int, body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: code,
			Status:     fmt.Sprintf("%d %s", code, http.StatusText(code)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	}

	httpClient := &http.Client{Transport: zoneRoundTripper(func(r *http.Request) (*http.Response, error) {
		const marker = "/managedZones"
		idx := strings.Index(r.URL.Path, marker)
		if idx == -1 {
			return jsonResp(r, http.StatusOK, "{}")
		}
		name := strings.Trim(r.URL.Path[idx+len(marker):], "/")

		switch r.Method {
		case http.MethodPost:
			zone := &dns.ManagedZone{}
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, zone))
			s.created = append(s.created, zone)
			s.zones[zone.Name] = zone
			return jsonResp(r, http.StatusOK, string(body))

		case http.MethodGet:
			if s.getErrorCode != 0 {
				return jsonResp(r, s.getErrorCode,
					fmt.Sprintf(`{"error":{"code":%d,"message":"boom"}}`, s.getErrorCode))
			}
			zone, ok := s.zones[name]
			if !ok {
				return jsonResp(r, http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`)
			}
			body, err := json.Marshal(zone)
			require.NoError(t, err)
			return jsonResp(r, http.StatusOK, string(body))

		case http.MethodPatch:
			zone := &dns.ManagedZone{}
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, zone))
			s.patched[name] = zone.Labels
			if existing, ok := s.zones[name]; ok {
				existing.Labels = zone.Labels
			}
			return jsonResp(r, http.StatusOK, `{"status":"done"}`)
		}

		return jsonResp(r, http.StatusOK, "{}")
	})}

	svc, err := dns.NewService(t.Context(), option.WithHTTPClient(httpClient), option.WithoutAuthentication())
	require.NoError(t, err)
	return svc
}
