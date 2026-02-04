package gcpprivateserviceconnect

import (
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/stretchr/testify/assert"
	"google.golang.org/api/googleapi"
)

func TestEnsureDNSDot(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "When name has no trailing dot it should add one",
			input:    "example.com",
			expected: "example.com.",
		},
		{
			name:     "When name already has trailing dot it should not add another",
			input:    "example.com.",
			expected: "example.com.",
		},
		{
			name:     "When name is empty it should add trailing dot",
			input:    "",
			expected: ".",
		},
		{
			name:     "When name is just a dot it should remain a single dot",
			input:    ".",
			expected: ".",
		},
		{
			name:     "When name has subdomain without dot it should add one",
			input:    "api.cluster.hypershift.local",
			expected: "api.cluster.hypershift.local.",
		},
		{
			name:     "When name has wildcard without dot it should add one",
			input:    "*.apps.cluster.example.com",
			expected: "*.apps.cluster.example.com.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ensureDNSDot(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When error is googleapi 404 it should return true",
			err:      &googleapi.Error{Code: 404, Message: "not found"},
			expected: true,
		},
		{
			name:     "When error is googleapi 403 it should return false",
			err:      &googleapi.Error{Code: 403, Message: "forbidden"},
			expected: false,
		},
		{
			name:     "When error is googleapi 500 it should return false",
			err:      &googleapi.Error{Code: 500, Message: "internal error"},
			expected: false,
		},
		{
			name:     "When error is googleapi 400 it should return false",
			err:      &googleapi.Error{Code: 400, Message: "bad request"},
			expected: false,
		},
		{
			name:     "When error message contains 'error 404' it should return true",
			err:      errors.New("error 404: resource not found"),
			expected: true,
		},
		{
			name:     "When error message contains 'notfound' it should return true",
			err:      errors.New("googleapi: Error 404: notfound"),
			expected: true,
		},
		{
			name:     "When error message contains 'not found' it should return true",
			err:      errors.New("the resource was not found"),
			expected: true,
		},
		{
			name:     "When error message contains 'NOT FOUND' in uppercase it should return true",
			err:      errors.New("RESOURCE NOT FOUND"),
			expected: true,
		},
		{
			name:     "When error is generic without 404 it should return false",
			err:      errors.New("connection timeout"),
			expected: false,
		},
		{
			name:     "When error is permission denied it should return false",
			err:      errors.New("permission denied"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNotFound(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTruncateName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "When name is shorter than max it should not truncate",
			input:    "short-name",
			maxLen:   63,
			expected: "short-name",
		},
		{
			name:     "When name equals max length it should not truncate",
			input:    "exactly-ten",
			maxLen:   11,
			expected: "exactly-ten",
		},
		{
			name:     "When name exceeds max length it should truncate",
			input:    "this-is-a-very-long-name-that-exceeds-the-maximum-length-allowed",
			maxLen:   20,
			expected: "this-is-a-very-long-",
		},
		{
			name:     "When max is 63 and name is 64 chars it should truncate to 63",
			input:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 64 chars
			maxLen:   63,
			expected: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 63 chars
		},
		{
			name:     "When max is 0 it should return empty string",
			input:    "any-name",
			maxLen:   0,
			expected: "",
		},
		{
			name:     "When name is empty it should return empty",
			input:    "",
			maxLen:   63,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateName(tt.input, tt.maxLen)
			assert.Equal(t, tt.expected, result)
			assert.LessOrEqual(t, len(result), tt.maxLen)
		})
	}
}

func TestGenerateZoneNames(t *testing.T) {
	tests := []struct {
		name                        string
		clusterName                 string
		baseDomain                  string
		expectedHypershiftLocalZone string
		expectedPublicIngressZone   string
		expectedPrivateIngressZone  string
		expectedIngressDNSName      string
	}{
		{
			name:                        "When given simple cluster and domain it should generate correct zone names",
			clusterName:                 "my-cluster",
			baseDomain:                  "example.com",
			expectedHypershiftLocalZone: "my-cluster-hypershift-local",
			expectedPublicIngressZone:   "example-com-public",
			expectedPrivateIngressZone:  "example-com-private",
			expectedIngressDNSName:      "example.com.",
		},
		{
			name:                        "When domain has multiple subdomains it should replace dots with hyphens",
			clusterName:                 "prod-cluster",
			baseDomain:                  "apps.us-east-1.aws.example.com",
			expectedHypershiftLocalZone: "prod-cluster-hypershift-local",
			expectedPublicIngressZone:   "apps-us-east-1-aws-example-com-public",
			expectedPrivateIngressZone:  "apps-us-east-1-aws-example-com-private",
			expectedIngressDNSName:      "apps.us-east-1.aws.example.com.",
		},
		{
			name:                        "When cluster name is very long it should truncate to 63 chars",
			clusterName:                 "this-is-an-extremely-long-cluster-name-that-exceeds-limits",
			baseDomain:                  "example.com",
			expectedHypershiftLocalZone: "this-is-an-extremely-long-cluster-name-that-exceeds-limits-hype",
			expectedPublicIngressZone:   "example-com-public",
			expectedPrivateIngressZone:  "example-com-private",
			expectedIngressDNSName:      "example.com.",
		},
		{
			name:                        "When base domain is very long it should truncate zone names",
			clusterName:                 "cluster",
			baseDomain:                  "very-long-subdomain.another-long-part.yet-another.example.com",
			expectedHypershiftLocalZone: "cluster-hypershift-local",
			expectedPublicIngressZone:   "very-long-subdomain-another-long-part-yet-another-examp-public",
			expectedPrivateIngressZone:  "very-long-subdomain-another-long-part-yet-another-examp-private",
			expectedIngressDNSName:      "very-long-subdomain.another-long-part.yet-another.example.com.",
		},
		{
			name:                        "When domain already has trailing dot it should be idempotent",
			clusterName:                 "test",
			baseDomain:                  "example.com.",
			expectedHypershiftLocalZone: "test-hypershift-local",
			expectedPublicIngressZone:   "example-com--public",
			expectedPrivateIngressZone:  "example-com--private",
			expectedIngressDNSName:      "example.com.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateZoneNames(tt.clusterName, tt.baseDomain)

			assert.Equal(t, tt.expectedHypershiftLocalZone, result.hypershiftLocalZoneName)
			assert.Equal(t, tt.expectedPublicIngressZone, result.publicIngressZoneName)
			assert.Equal(t, tt.expectedPrivateIngressZone, result.privateIngressZoneName)
			assert.Equal(t, tt.expectedIngressDNSName, result.ingressDNSName)

			// Verify all zone names are <= 63 characters (GCP DNS limit)
			assert.LessOrEqual(t, len(result.hypershiftLocalZoneName), 63, "hypershiftLocalZoneName exceeds 63 chars")
			assert.LessOrEqual(t, len(result.publicIngressZoneName), 63, "publicIngressZoneName exceeds 63 chars")
			assert.LessOrEqual(t, len(result.privateIngressZoneName), 63, "privateIngressZoneName exceeds 63 chars")
		})
	}
}

func TestValidateReconcileInput(t *testing.T) {
	validHCP := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				GCP: &hyperv1.GCPPlatformSpec{
					Project: "my-project",
					NetworkConfig: hyperv1.GCPNetworkConfig{
						Network: hyperv1.GCPResourceReference{
							Name: "my-vpc",
						},
					},
				},
			},
			DNS: hyperv1.DNSSpec{
				BaseDomain: "example.com",
			},
		},
	}

	tests := []struct {
		name          string
		hcp           *hyperv1.HostedControlPlane
		pscEndpointIP string
		expectError   bool
		errorContains string
	}{
		{
			name:          "When all inputs are valid it should return nil",
			hcp:           validHCP,
			pscEndpointIP: "10.0.1.5",
			expectError:   false,
		},
		{
			name: "When GCP platform spec is nil it should return error",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						GCP: nil,
					},
				},
			},
			pscEndpointIP: "10.0.1.5",
			expectError:   true,
			errorContains: "GCP platform spec is nil",
		},
		{
			name: "When baseDomain is empty it should return error",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "my-project",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								Network: hyperv1.GCPResourceReference{
									Name: "my-vpc",
								},
							},
						},
					},
					DNS: hyperv1.DNSSpec{
						BaseDomain: "",
					},
				},
			},
			pscEndpointIP: "10.0.1.5",
			expectError:   true,
			errorContains: "DNS baseDomain is required",
		},
		{
			name: "When GCP project is empty it should return error",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								Network: hyperv1.GCPResourceReference{
									Name: "my-vpc",
								},
							},
						},
					},
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
				},
			},
			pscEndpointIP: "10.0.1.5",
			expectError:   true,
			errorContains: "GCP project is required",
		},
		{
			name: "When VPC network name is empty it should return error",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "my-project",
							NetworkConfig: hyperv1.GCPNetworkConfig{
								Network: hyperv1.GCPResourceReference{
									Name: "",
								},
							},
						},
					},
					DNS: hyperv1.DNSSpec{
						BaseDomain: "example.com",
					},
				},
			},
			pscEndpointIP: "10.0.1.5",
			expectError:   true,
			errorContains: "VPC network name is required",
		},
		{
			name:          "When PSC endpoint IP is empty it should return error",
			hcp:           validHCP,
			pscEndpointIP: "",
			expectError:   true,
			errorContains: "PSC endpoint IP is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReconcileInput(tt.hcp, tt.pscEndpointIP)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestZoneNameLengthConstraints(t *testing.T) {
	// Test that generateZoneNames always produces valid GCP DNS zone names
	// GCP DNS zone names must be <= 63 characters
	tests := []struct {
		name        string
		clusterName string
		baseDomain  string
	}{
		{
			name:        "When cluster name is at maximum length it should still produce valid zone names",
			clusterName: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 63 chars
			baseDomain:  "example.com",
		},
		{
			name:        "When base domain is at maximum length it should still produce valid zone names",
			clusterName: "cluster",
			baseDomain:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.com", // very long
		},
		{
			name:        "When both are at maximum length it should still produce valid zone names",
			clusterName: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			baseDomain:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateZoneNames(tt.clusterName, tt.baseDomain)

			// All zone names must be <= 63 characters
			assert.LessOrEqual(t, len(result.hypershiftLocalZoneName), 63,
				"hypershiftLocalZoneName '%s' exceeds 63 chars (len=%d)",
				result.hypershiftLocalZoneName, len(result.hypershiftLocalZoneName))
			assert.LessOrEqual(t, len(result.publicIngressZoneName), 63,
				"publicIngressZoneName '%s' exceeds 63 chars (len=%d)",
				result.publicIngressZoneName, len(result.publicIngressZoneName))
			assert.LessOrEqual(t, len(result.privateIngressZoneName), 63,
				"privateIngressZoneName '%s' exceeds 63 chars (len=%d)",
				result.privateIngressZoneName, len(result.privateIngressZoneName))

			// Zone names should not be empty
			assert.NotEmpty(t, result.hypershiftLocalZoneName)
			assert.NotEmpty(t, result.publicIngressZoneName)
			assert.NotEmpty(t, result.privateIngressZoneName)
			assert.NotEmpty(t, result.ingressDNSName)
		})
	}
}
