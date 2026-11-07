package gcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

func TestDestroyInfraOptionsValidate(t *testing.T) {
	tests := []struct {
		name          string
		opts          *DestroyInfraOptions
		expectedError string
	}{
		{
			name: "When all required fields are provided it should pass validation",
			opts: &DestroyInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "test-infra-id",
				Region:    "us-central1",
			},
		},
		{
			name: "When project-id is missing it should return error",
			opts: &DestroyInfraOptions{
				ProjectID: "",
				InfraID:   "test-infra-id",
				Region:    "us-central1",
			},
			expectedError: "--project-id is required",
		},
		{
			name: "When infra-id is missing it should return error",
			opts: &DestroyInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "",
				Region:    "us-central1",
			},
			expectedError: "--infra-id is required",
		},
		{
			name: "When region is missing it should return error",
			opts: &DestroyInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "test-infra-id",
				Region:    "",
			},
			expectedError: "--region is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()

			if tt.expectedError != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.expectedError)
					return
				}
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestValidateNetwork(t *testing.T) {
	const networkLink = "https://www.googleapis.com/compute/v1/projects/openshift-gcp/global/networks/network-a-network"
	for _, tc := range []struct {
		name, resource string
		status         int
		patch          map[string]string
		exists         bool
		err            string
	}{
		{"When all resources match, it should validate the network", "", 0, nil, true, ""},
		{"When the VPC is missing, it should report its absence", "/networks/", http.StatusNotFound, nil, false, ""},
		{"When VPC lookup is forbidden, it should reject cleanup", "/networks/", http.StatusForbidden, nil, false, "failed to inspect GCP network"},
		{"When the VPC is unrecognized, it should reject cleanup", "/networks/", 0, map[string]string{"description": "Custom VPC"}, false, "not recognized"},
		{"When the VPC self link is missing, it should reject cleanup", "/networks/", 0, map[string]string{"selfLink": ""}, false, "no self link"},
		{"When the router belongs to another VPC, it should reject cleanup", "/routers/", 0, map[string]string{"network": networkLink + "-other"}, false, "router network-a-router does not belong"},
		{"When the subnet belongs to another VPC, it should reject cleanup", "/subnetworks/", 0, map[string]string{"network": networkLink + "-other"}, false, "subnet network-a-subnet does not belong"},
		{"When the firewall belongs to another VPC, it should reject cleanup", "/firewalls/", 0, map[string]string{"network": networkLink + "-other"}, false, "firewall rule network-a-allow-kubelet does not belong"},
		{"When the network reference belongs to another project, it should reject cleanup", "/routers/", 0, map[string]string{"network": strings.Replace(networkLink, "openshift-gcp", "other-project", 1)}, false, "does not belong"},
		{"When a child network reference is empty, it should reject cleanup", "/routers/", 0, map[string]string{"network": ""}, false, "does not belong"},
		{"When the router is missing, it should allow cleanup", "/routers/", http.StatusNotFound, nil, true, ""},
		{"When the subnet is missing, it should allow cleanup", "/subnetworks/", http.StatusNotFound, nil, true, ""},
		{"When the firewall is missing, it should allow cleanup", "/firewalls/", http.StatusNotFound, nil, true, ""},
		{"When router lookup is forbidden, it should reject cleanup", "/routers/", http.StatusForbidden, nil, false, "failed to inspect GCP router"},
		{"When subnet lookup is forbidden, it should reject cleanup", "/subnetworks/", http.StatusForbidden, nil, false, "failed to inspect GCP subnet"},
		{"When firewall lookup is forbidden, it should reject cleanup", "/firewalls/", http.StatusForbidden, nil, false, "failed to inspect GCP firewall"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("validation must not mutate resources: %s %s", r.Method, r.URL.Path)
				}
				body := map[string]string{
					"name":    r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:],
					"network": networkLink, "selfLink": networkLink,
					"description": "HyperShift VPC for cluster network-a",
				}
				if tc.resource != "" && strings.Contains(r.URL.Path, tc.resource) {
					if tc.status != 0 {
						http.Error(w, "lookup failed", tc.status)
						return
					}
					for field, value := range tc.patch {
						body[field] = value
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(body); err != nil {
					t.Errorf("write fake Compute response: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			service, err := compute.NewService(t.Context(), option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
			g.Expect(err).NotTo(HaveOccurred(), "initialize local Compute client")
			manager := &NetworkManager{projectID: "openshift-gcp", infraID: "network-a", region: "us-central1", computeService: service}
			exists, err := manager.validateNetwork(t.Context())
			g.Expect(exists).To(Equal(tc.exists), "network validation result")
			if tc.err != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.err)), "reject unvalidated resources")
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "accept matching or absent resources")
			}
		})
	}
}

func TestRun(t *testing.T) {
	const networkLink = "https://www.googleapis.com/compute/v1/projects/openshift-gcp/global/networks/network-a-network"
	credentials := filepath.Join(t.TempDir(), "adc.json")
	NewGomegaWithT(t).Expect(os.WriteFile(credentials, []byte(`{"type":"authorized_user","client_id":"unit-test","client_secret":"unit-test","refresh_token":"unit-test"}`), 0600)).To(Succeed())
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credentials)
	t.Setenv("GOOGLE_API_USE_MTLS_ENDPOINT", "never")
	t.Setenv("GOOGLE_API_USE_CLIENT_CERTIFICATE", "false")
	for _, tc := range []struct {
		name, resource string
		status         int
		patch          map[string]string
		mutations      int32
		err            string
	}{
		{"When resources match, it should delete networking", "", 0, nil, 5, ""},
		{"When the VPC is missing, it should skip networking", "/networks/", http.StatusNotFound, nil, 0, ""},
		{"When lookup is forbidden, it should reject cleanup", "/networks/", http.StatusForbidden, nil, 0, "cannot validate GCP networking"},
		{"When the VPC is unrecognized, it should reject cleanup", "/networks/", 0, map[string]string{"description": "Custom VPC"}, 0, "not recognized"},
		{"When the router belongs to another VPC, it should reject cleanup", "/routers/", 0, map[string]string{"network": networkLink + "-other"}, 0, "router network-a-router does not belong"},
		{"When the subnet belongs to another VPC, it should reject cleanup", "/subnetworks/", 0, map[string]string{"network": networkLink + "-other"}, 0, "subnet network-a-subnet does not belong"},
		{"When the firewall belongs to another VPC, it should reject cleanup", "/firewalls/", 0, map[string]string{"network": networkLink + "-other"}, 0, "firewall rule network-a-allow-kubelet does not belong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			var mutations atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"name": "cleanup", "status": "DONE"}
				switch {
				case r.URL.Path == "/token":
					body = map[string]any{"access_token": "unit-test", "token_type": "Bearer", "expires_in": 3600}
				case strings.Contains(r.URL.Path, "/operations/"):
				case r.Method == http.MethodDelete || r.Method == http.MethodPatch:
					mutations.Add(1)
				case r.Method == http.MethodGet:
					body = map[string]any{
						"name": r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "network": networkLink,
						"selfLink": networkLink, "description": "HyperShift VPC for cluster network-a",
						"nats": []map[string]string{{"name": "network-a-nat"}},
					}
					if tc.resource != "" && strings.Contains(r.URL.Path, tc.resource) {
						if tc.status != 0 {
							http.Error(w, "lookup failed", tc.status)
							return
						}
						for field, value := range tc.patch {
							body[field] = value
						}
					}
				default:
					t.Errorf("unexpected Compute request: %s %s", r.Method, r.URL.Path)
				}
				if err := json.NewEncoder(w).Encode(body); err != nil {
					t.Errorf("write fake Compute response: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			// SDK clients clone the default transport; redirect them and token requests locally.
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig.ServerName = "127.0.0.1"
			transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			original := http.DefaultTransport
			http.DefaultTransport = transport
			t.Cleanup(func() {
				http.DefaultTransport = original
				transport.CloseIdleConnections()
			})
			opts := &DestroyInfraOptions{ProjectID: "openshift-gcp", Region: "us-central1", InfraID: "network-a"}
			err := opts.Run(t.Context(), logr.Discard())
			if tc.err != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.err)), "standalone cleanup validation")
			} else {
				g.Expect(err).NotTo(HaveOccurred(), "standalone cleanup")
			}
			g.Expect(mutations.Load()).To(Equal(tc.mutations), "only validated networking may be mutated")
		})
	}
}

func TestFormatOperationErrors(t *testing.T) {
	tests := []struct {
		name     string
		errors   []*compute.OperationErrorErrors
		expected string
	}{
		{
			name:     "When errors is nil, it should return unknown error",
			errors:   nil,
			expected: "unknown error",
		},
		{
			name:     "When errors is empty, it should return unknown error",
			errors:   []*compute.OperationErrorErrors{},
			expected: "unknown error",
		},
		{
			name: "When single error, it should format correctly",
			errors: []*compute.OperationErrorErrors{
				{Code: "RESOURCE_IN_USE", Message: "Resource is in use"},
			},
			expected: "[RESOURCE_IN_USE: Resource is in use]",
		},
		{
			name: "When multiple errors, it should format all",
			errors: []*compute.OperationErrorErrors{
				{Code: "ERROR_1", Message: "First error"},
				{Code: "ERROR_2", Message: "Second error"},
			},
			expected: "[ERROR_1: First error, ERROR_2: Second error]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatOperationErrors(tt.errors)
			if got != tt.expected {
				t.Errorf("formatOperationErrors() = %q, want %q", got, tt.expected)
			}
		})
	}
}
