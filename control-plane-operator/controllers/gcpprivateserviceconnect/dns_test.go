package gcpprivateserviceconnect

import (
	"errors"
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
)

func TestEnsureDNSDot(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "When name has no trailing dot, it should add one",
			input:    "example.com",
			expected: "example.com.",
		},
		{
			name:     "When name already has trailing dot, it should not add another",
			input:    "example.com.",
			expected: "example.com.",
		},
		{
			name:     "When name is empty, it should add trailing dot",
			input:    "",
			expected: ".",
		},
		{
			name:     "When name is just a dot, it should remain a single dot",
			input:    ".",
			expected: ".",
		},
		{
			name:     "When name has subdomain without dot, it should add one",
			input:    "api.cluster.hypershift.local",
			expected: "api.cluster.hypershift.local.",
		},
		{
			name:     "When name has wildcard without dot, it should add one",
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
			name:     "When error is googleapi 404, it should return true",
			err:      &googleapi.Error{Code: 404, Message: "not found"},
			expected: true,
		},
		{
			name:     "When error is googleapi 403, it should return false",
			err:      &googleapi.Error{Code: 403, Message: "forbidden"},
			expected: false,
		},
		{
			name:     "When error is googleapi 500, it should return false",
			err:      &googleapi.Error{Code: 500, Message: "internal error"},
			expected: false,
		},
		{
			name:     "When error is googleapi 400, it should return false",
			err:      &googleapi.Error{Code: 400, Message: "bad request"},
			expected: false,
		},
		{
			name:     "When error message contains 'error 404', it should return true",
			err:      errors.New("error 404: resource not found"),
			expected: true,
		},
		{
			name:     "When error message contains 'notfound', it should return true",
			err:      errors.New("googleapi: Error 404: notfound"),
			expected: true,
		},
		{
			name:     "When error message contains 'not found', it should return true",
			err:      errors.New("the resource was not found"),
			expected: true,
		},
		{
			name:     "When error message contains 'NOT FOUND' in uppercase, it should return true",
			err:      errors.New("RESOURCE NOT FOUND"),
			expected: true,
		},
		{
			name:     "When error is a wrapped googleapi 404, it should return true",
			err:      fmt.Errorf("operation failed: %w", &googleapi.Error{Code: 404, Message: "not found"}),
			expected: true,
		},
		{
			name:     "When error is a wrapped googleapi 500, it should return false",
			err:      fmt.Errorf("operation failed: %w", &googleapi.Error{Code: 500, Message: "internal error"}),
			expected: false,
		},
		{
			name:     "When error is generic without 404, it should return false",
			err:      errors.New("connection timeout"),
			expected: false,
		},
		{
			name:     "When error is permission denied, it should return false",
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
			name:     "When name is shorter than max, it should not truncate",
			input:    "short-name",
			maxLen:   63,
			expected: "short-name",
		},
		{
			name:     "When name equals max length, it should not truncate",
			input:    "exactly-ten",
			maxLen:   11,
			expected: "exactly-ten",
		},
		{
			name:     "When name exceeds max length, it should truncate",
			input:    "this-is-a-very-long-name-that-exceeds-the-maximum-length-allowed",
			maxLen:   20,
			expected: "this-is-a-very-long-",
		},
		{
			name:     "When max is 63 and name is 64 chars, it should truncate to 63",
			input:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 64 chars
			maxLen:   63,
			expected: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", // 63 chars
		},
		{
			name:     "When max is 0, it should return empty string",
			input:    "any-name",
			maxLen:   0,
			expected: "",
		},
		{
			name:     "When name is empty, it should return empty",
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
			result, err := generateZoneNames(tt.clusterName, tt.baseDomain)
			require.NoError(t, err, "generateZoneNames should not return error for valid inputs")

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
			name:          "When all inputs are valid, it should return nil",
			hcp:           validHCP,
			pscEndpointIP: "10.0.1.5",
			expectError:   false,
		},
		{
			name: "When GCP platform spec is nil, it should return error",
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
			name: "When baseDomain is empty, it should return error",
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
			name: "When GCP project is empty, it should return error",
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
			name: "When VPC network name is empty, it should return error",
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
			name:          "When PSC endpoint IP is empty, it should return error",
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
			result, err := generateZoneNames(tt.clusterName, tt.baseDomain)
			require.NoError(t, err, "generateZoneNames should not return error for valid inputs")

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

func TestValidateZoneName(t *testing.T) {
	tests := []struct {
		name        string
		zoneName    string
		expectError bool
	}{
		{
			name:        "When zone name starts with lowercase letter, it should be valid",
			zoneName:    "my-zone",
			expectError: false,
		},
		{
			name:        "When zone name contains only lowercase letters, it should be valid",
			zoneName:    "myzone",
			expectError: false,
		},
		{
			name:        "When zone name contains lowercase letters and numbers, it should be valid",
			zoneName:    "my-zone-123",
			expectError: false,
		},
		{
			name:        "When zone name contains hyphens, it should be valid",
			zoneName:    "my-cluster-hypershift-local",
			expectError: false,
		},
		{
			name:        "When zone name starts with 'in-' (managed service pattern), it should be valid",
			zoneName:    "in-cluster-abc123-public",
			expectError: false,
		},
		{
			name:        "When zone name starts with digit, it should be invalid",
			zoneName:    "123-zone",
			expectError: true,
		},
		{
			name:        "When zone name starts with hyphen, it should be invalid",
			zoneName:    "-my-zone",
			expectError: true,
		},
		{
			name:        "When zone name contains uppercase letters, it should be invalid",
			zoneName:    "My-Zone",
			expectError: true,
		},
		{
			name:        "When zone name contains underscore, it should be invalid",
			zoneName:    "my_zone",
			expectError: true,
		},
		{
			name:        "When zone name contains dot, it should be invalid",
			zoneName:    "my.zone",
			expectError: true,
		},
		{
			name:        "When zone name is empty, it should be invalid",
			zoneName:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateZoneName(tt.zoneName)
			if tt.expectError {
				assert.Error(t, err, "validateZoneName should return error for invalid zone name: %s", tt.zoneName)
			} else {
				assert.NoError(t, err, "validateZoneName should not return error for valid zone name: %s", tt.zoneName)
			}
		})
	}
}

func TestGenerateZoneNamesValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		clusterName   string
		baseDomain    string
		errorContains string
	}{
		{
			name:          "When cluster name starts with digit it should return error",
			clusterName:   "123-cluster",
			baseDomain:    "example.com",
			errorContains: "invalid hypershift.local zone name",
		},
		{
			name:          "When cluster name starts with hyphen it should return error",
			clusterName:   "-cluster",
			baseDomain:    "example.com",
			errorContains: "invalid hypershift.local zone name",
		},
		{
			name:          "When base domain starts with digit it should return error for ingress zones",
			clusterName:   "my-cluster",
			baseDomain:    "123.example.com",
			errorContains: "invalid public ingress zone name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := generateZoneNames(tt.clusterName, tt.baseDomain)
			require.Error(t, err, "generateZoneNames should return error")
			assert.Contains(t, err.Error(), tt.errorContains)
		})
	}
}

const testProjectID = "my-gcp-project"

func TestEnsureZone(t *testing.T) {
	desiredLabels := map[string]string{
		"goog-partner-solution": "openshift",
		ownershipLabelKey:       realClusterID,
	}
	spec := zoneSpec{
		name:          "my-cluster-hypershift-local",
		dnsName:       "my-cluster.hypershift.local",
		visibility:    "private",
		vpcNetworkURL: "https://www.googleapis.com/compute/v1/projects/my-gcp-project/global/networks/my-vpc",
		labels:        desiredLabels,
	}

	t.Run("When the zone does not exist, it should create it with the ownership and resource labels", func(t *testing.T) {
		server := newFakeZoneServer()

		zone, err := ensureZone(t.Context(), server.service(t), testProjectID, spec, realClusterID)
		require.NoError(t, err)
		assert.Equal(t, spec.name, zone.Name)

		require.Len(t, server.created, 1, "exactly one zone should be created")
		assert.Equal(t, desiredLabels, server.created[0].Labels)
		assert.Equal(t, "my-cluster.hypershift.local.", server.created[0].DnsName, "DNS name should be dot-terminated")
		require.NotNil(t, server.created[0].PrivateVisibilityConfig)
		assert.Empty(t, server.patched, "a newly created zone should not be patched")
	})

	t.Run("When the zone is owned by this cluster and labels match, it should not issue a patch", func(t *testing.T) {
		server := newFakeZoneServer(&dns.ManagedZone{Name: spec.name, Labels: desiredLabels})

		zone, err := ensureZone(t.Context(), server.service(t), testProjectID, spec, realClusterID)
		require.NoError(t, err)
		assert.Equal(t, spec.name, zone.Name)
		assert.Empty(t, server.created, "an existing zone should not be re-created")
		assert.Empty(t, server.patched, "an up-to-date zone should not be patched")
	})

	t.Run("When the zone is owned by this cluster and a label drifted, it should patch and preserve unrelated labels", func(t *testing.T) {
		server := newFakeZoneServer(&dns.ManagedZone{
			Name: spec.name,
			Labels: map[string]string{
				ownershipLabelKey: realClusterID,
				"customer-label":  "keep",
			},
		})

		_, err := ensureZone(t.Context(), server.service(t), testProjectID, spec, realClusterID)
		require.NoError(t, err)
		assert.Empty(t, server.created)
		require.Contains(t, server.patched, spec.name)
		assert.Equal(t, map[string]string{
			"goog-partner-solution": "openshift",
			ownershipLabelKey:       realClusterID,
			"customer-label":        "keep",
		}, server.patched[spec.name], "the unrelated customer label must survive the patch")
	})

	t.Run("When the zone exists but has no ownership label, it should not adopt or mutate it", func(t *testing.T) {
		server := newFakeZoneServer(&dns.ManagedZone{Name: spec.name, Labels: map[string]string{"customer-label": "keep"}})

		_, err := ensureZone(t.Context(), server.service(t), testProjectID, spec, realClusterID)
		require.ErrorIs(t, err, errZoneUnowned)
		assert.Empty(t, server.created)
		assert.Empty(t, server.patched, "an unlabeled zone must never be labeled")
	})

	t.Run("When the zone is owned by another cluster, it should return a conflict without mutating it", func(t *testing.T) {
		server := newFakeZoneServer(&dns.ManagedZone{
			Name:   spec.name,
			Labels: map[string]string{ownershipLabelKey: "8c7d6e5f-4a3b-4c2d-9e1f-0a1b2c3d4e5f"},
		})

		_, err := ensureZone(t.Context(), server.service(t), testProjectID, spec, realClusterID)
		require.ErrorIs(t, err, errZoneOwnedByOther)
		assert.Empty(t, server.created)
		assert.Empty(t, server.patched, "another cluster's ownership label must never be overwritten")
	})

	t.Run("When the zone lookup fails for a reason other than not found, it should not create a zone", func(t *testing.T) {
		server := newFakeZoneServer()
		server.getErrorCode = 403

		_, err := ensureZone(t.Context(), server.service(t), testProjectID, spec, realClusterID)
		require.Error(t, err)
		assert.Empty(t, server.created, "a failed lookup must not be treated as an absent zone")
	})
}

func TestReconcileZoneLabels(t *testing.T) {
	desired := map[string]string{ownershipLabelKey: realClusterID}

	t.Run("When the zone already carries the desired labels, it should return without an API call", func(t *testing.T) {
		zone := &dns.ManagedZone{Name: "zone-a", Labels: desired}
		server := newFakeZoneServer(zone)

		got, err := reconcileZoneLabels(t.Context(), server.service(t), testProjectID, zone, desired)
		require.NoError(t, err)
		assert.Equal(t, desired, got.Labels)
		assert.Empty(t, server.patched)
	})

	t.Run("When a desired label is missing, it should patch the zone and return the merged labels", func(t *testing.T) {
		zone := &dns.ManagedZone{Name: "zone-a", Labels: map[string]string{"customer-label": "keep"}}
		server := newFakeZoneServer(zone)

		got, err := reconcileZoneLabels(t.Context(), server.service(t), testProjectID, zone, desired)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"customer-label": "keep", ownershipLabelKey: realClusterID}, got.Labels)
		assert.Equal(t, got.Labels, server.patched["zone-a"])
	})
}

func TestCreateZonesIfNeeded(t *testing.T) {
	t.Run("When no zones exist, it should create all three carrying the ownership label", func(t *testing.T) {
		server := newFakeZoneServer()
		labels := zoneLabels(map[string]string{"goog-partner-solution": "openshift"}, realClusterID)

		local, public, private, err := createZonesIfNeeded(
			t.Context(), server.service(t), testProjectID,
			"my-cluster-hypershift-local", "in-my-cluster-public", "in-my-cluster-private",
			"my-cluster.hypershift.local", "in.my-cluster.example.com",
			"https://www.googleapis.com/compute/v1/projects/my-gcp-project/global/networks/my-vpc",
			labels, realClusterID,
		)
		require.NoError(t, err)
		assert.Equal(t, "my-cluster-hypershift-local", local.Name)
		assert.Equal(t, "in-my-cluster-public", public.Name)
		assert.Equal(t, "in-my-cluster-private", private.Name)

		require.Len(t, server.created, 3, "all three zones should be created")
		for _, zone := range server.created {
			assert.Equal(t, realClusterID, zone.Labels[ownershipLabelKey],
				"zone %q should carry the ownership label", zone.Name)
			assert.Equal(t, "openshift", zone.Labels["goog-partner-solution"],
				"zone %q should carry the attribution label", zone.Name)
		}
	})

	t.Run("When an ingress zone is owned by another cluster, it should fail without creating the remaining zone", func(t *testing.T) {
		server := newFakeZoneServer(&dns.ManagedZone{
			Name:   "in-my-cluster-public",
			Labels: map[string]string{ownershipLabelKey: "8c7d6e5f-4a3b-4c2d-9e1f-0a1b2c3d4e5f"},
		})
		labels := zoneLabels(nil, realClusterID)

		_, _, _, err := createZonesIfNeeded(
			t.Context(), server.service(t), testProjectID,
			"my-cluster-hypershift-local", "in-my-cluster-public", "in-my-cluster-private",
			"my-cluster.hypershift.local", "in.my-cluster.example.com", "",
			labels, realClusterID,
		)
		require.ErrorIs(t, err, errZoneOwnedByOther)

		created := make([]string, 0, len(server.created))
		for _, zone := range server.created {
			created = append(created, zone.Name)
		}
		assert.Equal(t, []string{"my-cluster-hypershift-local"}, created,
			"reconciliation should stop at the conflicting zone")
	})
}

func TestDeleteZoneIfOwned(t *testing.T) {
	// fakeDNSService (psc_endpoint_controller_test.go) is used here rather than
	// fakeZoneServer because it also serves the record-list and change calls that
	// deleting a zone makes.
	const otherOwner = "8c7d6e5f-4a3b-4c2d-9e1f-0a1b2c3d4e5f"

	tests := []struct {
		name            string
		existing        *dns.ManagedZone
		expectedErr     error
		expectedDeletes []string
	}{
		{
			name: "When the zone is owned by this cluster, it should delete it",
			existing: &dns.ManagedZone{
				Name:   "my-zone",
				Labels: map[string]string{ownershipLabelKey: realClusterID},
			},
			expectedDeletes: []string{"my-zone"},
		},
		{
			name: "When the zone reports a numeric ID, it should delete by ID rather than by name",
			existing: &dns.ManagedZone{
				Name:   "my-zone",
				Id:     8675309,
				Labels: map[string]string{ownershipLabelKey: realClusterID},
			},
			expectedDeletes: []string{"8675309"},
		},
		{
			name: "When the zone is owned by another cluster, it should return a conflict and delete nothing",
			existing: &dns.ManagedZone{
				Name:   "my-zone",
				Labels: map[string]string{ownershipLabelKey: otherOwner},
			},
			expectedErr: errZoneOwnedByOther,
		},
		{
			name:        "When the zone has no ownership label, it should refuse to delete it",
			existing:    &dns.ManagedZone{Name: "my-zone"},
			expectedErr: errZoneUnowned,
		},
		{
			name:     "When the zone does not exist, it should be a no-op",
			existing: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := map[string]*dns.ManagedZone{}
			if tt.existing != nil {
				existing[tt.existing.Name] = tt.existing
			}
			var deletes []string

			err := deleteZoneIfOwned(t.Context(), fakeDNSService(t, existing, &deletes),
				testProjectID, "my-zone", realClusterID)

			if tt.expectedErr != nil {
				require.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
			assert.ElementsMatch(t, tt.expectedDeletes, deletes)
		})
	}
}

func TestReconcileDNS(t *testing.T) {
	// ReconcileDNS resolves zone ownership before constructing a DNS client, so the
	// fail-closed paths are reachable without GCP credentials.
	validHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "my-cluster"},
			Spec: hyperv1.HostedControlPlaneSpec{
				ClusterID: realClusterID,
				DNS:       hyperv1.DNSSpec{BaseDomain: "in.my-cluster.example.com"},
				Platform: hyperv1.PlatformSpec{
					GCP: &hyperv1.GCPPlatformSpec{
						Project: testProjectID,
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network: hyperv1.GCPResourceReference{Name: "my-vpc"},
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name          string
		mutate        func(*hyperv1.HostedControlPlane)
		errorContains string
	}{
		{
			name:          "When clusterID is empty, it should fail before contacting GCP",
			mutate:        func(hcp *hyperv1.HostedControlPlane) { hcp.Spec.ClusterID = "" },
			errorContains: "has no clusterID",
		},
		{
			name:          "When clusterID is not a UUID, it should fail before contacting GCP",
			mutate:        func(hcp *hyperv1.HostedControlPlane) { hcp.Spec.ClusterID = "not-a-uuid" },
			errorContains: "is not a valid UUID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := validHCP()
			tt.mutate(hcp)

			result, err := ReconcileDNS(t.Context(), hcp, "10.0.0.5")
			require.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), tt.errorContains)
		})
	}
}
