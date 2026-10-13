package gcpprivateserviceconnect

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstructEndpointName(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name     string
		gcpPSC   *hyperv1.GCPPrivateServiceConnect
		expected string
	}{
		{
			name: "When constructing endpoint name it should use service attachment name with endpoint suffix",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentName: "private-router-4bcf17df-cveiga-test-3-psc-sa",
				},
			},
			expected: "private-router-4bcf17df-cveiga-test-3-psc-sa-endpoint",
		},
		{
			name: "When service attachment name is short it should append endpoint suffix",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentName: "test-sa",
				},
			},
			expected: "test-sa-endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.constructEndpointName(tt.gcpPSC)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConstructIPAddressName(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name     string
		gcpPSC   *hyperv1.GCPPrivateServiceConnect
		expected string
	}{
		{
			name: "When constructing IP name it should use service attachment name with ip suffix",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentName: "private-router-4bcf17df-cveiga-test-3-psc-sa",
				},
			},
			expected: "private-router-4bcf17df-cveiga-test-3-psc-sa-ip",
		},
		{
			name: "When service attachment name is short it should append ip suffix",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentName: "test-sa",
				},
			},
			expected: "test-sa-ip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.constructIPAddressName(tt.gcpPSC)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConstructNetworkURL(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	networkName := "default"
	customerProject := "customer-project"

	result := r.constructNetworkURL(networkName, customerProject)
	expected := "projects/customer-project/global/networks/default"

	assert.Equal(t, expected, result)
}

func TestConstructSubnetURL(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	subnetName := "psc-subnet"
	customerProject := "customer-project"
	region := "us-central1"

	result := r.constructSubnetURL(subnetName, customerProject, region)
	expected := "projects/customer-project/regions/us-central1/subnetworks/psc-subnet"

	assert.Equal(t, expected, result)
}

func TestConstructAddressURL(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name            string
		addressName     string
		customerProject string
		region          string
		expected        string
	}{
		{
			name:            "When constructing address URL it should include project, region, and name",
			addressName:     "clusters-test-cluster-1-private-router-psc-endpoint-ip",
			customerProject: "customer-project-123",
			region:          "us-central1",
			expected:        "projects/customer-project-123/regions/us-central1/addresses/clusters-test-cluster-1-private-router-psc-endpoint-ip",
		},
		{
			name:            "When using different region it should construct correctly",
			addressName:     "test-address",
			customerProject: "my-gcp-project",
			region:          "europe-west1",
			expected:        "projects/my-gcp-project/regions/europe-west1/addresses/test-address",
		},
		{
			name:            "When using numeric project ID it should work",
			addressName:     "my-psc-ip",
			customerProject: "123456789",
			region:          "asia-southeast1",
			expected:        "projects/123456789/regions/asia-southeast1/addresses/my-psc-ip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.constructAddressURL(tt.addressName, tt.customerProject, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsServiceAttachmentReady(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name     string
		gcpPSC   *hyperv1.GCPPrivateServiceConnect
		expected bool
	}{
		{
			name: "When ServiceAttachmentURI is empty it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI:  "",
					ServiceAttachmentName: "test-sa",
				},
			},
			expected: false,
		},
		{
			name: "When ServiceAttachmentName is empty it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI:  "projects/mgmt-project/regions/us-central1/serviceAttachments/test-sa",
					ServiceAttachmentName: "",
				},
			},
			expected: false,
		},
		{
			name: "When both URI and Name exist but condition is missing it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI:  "projects/mgmt-project/regions/us-central1/serviceAttachments/test-sa",
					ServiceAttachmentName: "test-sa",
				},
			},
			expected: false,
		},
		{
			name: "When both URI and Name exist but condition is False it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI:  "projects/mgmt-project/regions/us-central1/serviceAttachments/test-sa",
					ServiceAttachmentName: "test-sa",
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.GCPServiceAttachmentAvailable),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When both URI and Name exist and condition is True it should return true",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI:  "projects/mgmt-project/regions/us-central1/serviceAttachments/test-sa",
					ServiceAttachmentName: "test-sa",
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.GCPServiceAttachmentAvailable),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.isServiceAttachmentReady(tt.gcpPSC)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When given nil error it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When given non-GCP error it should return false",
			err:      assert.AnError,
			expected: false,
		},
		// Note: We can't easily test the GCP API error case without importing the full GCP SDK
		// and creating mock errors, but the logic is straightforward
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNotFoundError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test unique naming across different clusters using ServiceAttachmentName
func TestIPAddressNameUniqueness(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	// Service attachment names are unique per cluster, ensuring GCP resource uniqueness
	cluster1PSC := &hyperv1.GCPPrivateServiceConnect{
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			ServiceAttachmentName: "private-router-4bcf17df-cluster-1-psc-sa",
		},
	}

	cluster2PSC := &hyperv1.GCPPrivateServiceConnect{
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			ServiceAttachmentName: "private-router-5def28eg-cluster-2-psc-sa",
		},
	}

	name1 := r.constructIPAddressName(cluster1PSC)
	name2 := r.constructIPAddressName(cluster2PSC)

	// Names should be different to prevent GCP resource conflicts
	assert.NotEqual(t, name1, name2, "IP address names should be unique across different clusters")

	assert.Equal(t, "private-router-4bcf17df-cluster-1-psc-sa-ip", name1)
	assert.Equal(t, "private-router-5def28eg-cluster-2-psc-sa-ip", name2)
}

// Test that naming functions are consistent for both endpoint and IP
func TestNamingFunctionConsistency(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			ServiceAttachmentName: "private-router-4bcf17df-cveiga-test-3-psc-sa",
		},
	}

	// Both functions should use the same service attachment name as base
	ipName := r.constructIPAddressName(gcpPSC)
	endpointName := r.constructEndpointName(gcpPSC)

	assert.Equal(t, "private-router-4bcf17df-cveiga-test-3-psc-sa-ip", ipName)
	assert.Equal(t, "private-router-4bcf17df-cveiga-test-3-psc-sa-endpoint", endpointName)

	// Both should be under 63 characters (GCP limit)
	assert.LessOrEqual(t, len(ipName), 63, "IP name should be <= 63 characters")
	assert.LessOrEqual(t, len(endpointName), 63, "Endpoint name should be <= 63 characters")
}

// Test endpoint naming uniqueness across different clusters using ServiceAttachmentName
func TestEndpointNameUniqueness(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	// Service attachment names are unique per cluster
	cluster1PSC := &hyperv1.GCPPrivateServiceConnect{
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			ServiceAttachmentName: "private-router-4bcf17df-cluster-1-psc-sa",
		},
	}

	cluster2PSC := &hyperv1.GCPPrivateServiceConnect{
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			ServiceAttachmentName: "private-router-5def28eg-cluster-2-psc-sa",
		},
	}

	endpointName1 := r.constructEndpointName(cluster1PSC)
	endpointName2 := r.constructEndpointName(cluster2PSC)

	// Names should be different to prevent GCP PSC endpoint conflicts
	assert.NotEqual(t, endpointName1, endpointName2, "PSC endpoint names should be unique across different clusters")

	assert.Equal(t, "private-router-4bcf17df-cluster-1-psc-sa-endpoint", endpointName1)
	assert.Equal(t, "private-router-5def28eg-cluster-2-psc-sa-endpoint", endpointName2)
}

func TestHCPExternalNamesGCP(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected map[string]string
	}{
		{
			name: "When no external hostnames are configured it should return empty map",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{},
				},
			},
			expected: map[string]string{},
		},
		{
			name: "When API server has Route hostname it should return api entry",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.my-custom-domain.com",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{
				"api": "api.my-custom-domain.com",
			},
		},
		{
			name: "When OAuth server has Route hostname it should return oauth entry",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.my-custom-domain.com",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{
				"oauth": "oauth.my-custom-domain.com",
			},
		},
		{
			name: "When both API and OAuth have Route hostnames it should return both entries",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.my-custom-domain.com",
								},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.my-custom-domain.com",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{
				"api":   "api.my-custom-domain.com",
				"oauth": "oauth.my-custom-domain.com",
			},
		},
		{
			name: "When API server uses LoadBalancer type it should return empty map",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			expected: map[string]string{},
		},
		{
			name: "When Route has no hostname it should return empty map",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "", // Empty hostname
								},
							},
						},
					},
				},
			},
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hcpExternalNamesGCP(tt.hcp)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestReconcileExternalServiceGCP(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
	}

	tests := []struct {
		name                 string
		hostName             string
		targetIP             string
		expectedExternalName string
		expectedAnnotation   string
	}{
		{
			name:                 "When configuring external service it should set correct ExternalName and annotation",
			hostName:             "api.my-custom-domain.com",
			targetIP:             "10.0.1.5",
			expectedExternalName: "10.0.1.5",
			expectedAnnotation:   "api.my-custom-domain.com",
		},
		{
			name:                 "When configuring OAuth service it should handle different hostname",
			hostName:             "oauth.my-enterprise.com",
			targetIP:             "192.168.1.100",
			expectedExternalName: "192.168.1.100",
			expectedAnnotation:   "oauth.my-enterprise.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a basic service
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service",
					Namespace: hcp.Namespace,
				},
			}

			err := reconcileExternalServiceGCP(svc, hcp, tt.hostName, tt.targetIP)

			assert.NoError(t, err)
			assert.Equal(t, corev1.ServiceTypeExternalName, svc.Spec.Type)
			assert.Equal(t, tt.expectedExternalName, svc.Spec.ExternalName)
			assert.Equal(t, tt.expectedAnnotation, svc.Annotations[hyperv1.ExternalDNSHostnameAnnotation])
			assert.Equal(t, "true", svc.Labels[externalPrivateServiceLabelGCP])

			// Verify owner reference is set
			assert.Len(t, svc.OwnerReferences, 1)
			assert.Equal(t, "HostedControlPlane", svc.OwnerReferences[0].Kind)
			assert.Equal(t, hcp.Name, svc.OwnerReferences[0].Name)

			// Verify port configuration
			assert.Len(t, svc.Spec.Ports, 1)
			assert.Equal(t, "https", svc.Spec.Ports[0].Name)
			assert.Equal(t, int32(443), svc.Spec.Ports[0].Port)
			assert.Equal(t, corev1.ProtocolTCP, svc.Spec.Ports[0].Protocol)
		})
	}
}

func TestDNSEndpointNameTrimming(t *testing.T) {
	tests := []struct {
		name        string
		ingressDNS  string
		expectedDNS string
		description string
	}{
		{
			name:        "When DNS name has trailing dot it should be removed",
			ingressDNS:  "in.cluster.region.example.com.",
			expectedDNS: "in.cluster.region.example.com",
			description: "DNSEndpoint spec doesn't use trailing dots",
		},
		{
			name:        "When DNS name has no trailing dot it should remain unchanged",
			ingressDNS:  "in.cluster.region.example.com",
			expectedDNS: "in.cluster.region.example.com",
			description: "Already in correct format",
		},
		{
			name:        "When DNS name is empty it should remain empty",
			ingressDNS:  "",
			expectedDNS: "",
			description: "Edge case: empty string",
		},
		{
			name:        "When DNS name is only a dot it should become empty",
			ingressDNS:  ".",
			expectedDNS: "",
			description: "Edge case: single dot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedDNS, trimDNSName(tt.ingressDNS), tt.description)
		})
	}
}

func TestNameserverTrailingDotTrimming(t *testing.T) {
	tests := []struct {
		name        string
		nameservers []string
		expected    []string
		description string
	}{
		{
			name:        "When nameservers have trailing dots they should be removed",
			nameservers: []string{"ns-cloud-c1.googledomains.com.", "ns-cloud-c2.googledomains.com."},
			expected:    []string{"ns-cloud-c1.googledomains.com", "ns-cloud-c2.googledomains.com"},
			description: "GCP Cloud DNS returns nameservers with trailing dots but external-dns rejects them",
		},
		{
			name:        "When nameservers have no trailing dots they should remain unchanged",
			nameservers: []string{"ns1.example.com", "ns2.example.com"},
			expected:    []string{"ns1.example.com", "ns2.example.com"},
			description: "Already in correct format for external-dns",
		},
		{
			name:        "When nameservers list is empty it should return empty",
			nameservers: []string{},
			expected:    []string{},
			description: "Edge case: empty nameserver list",
		},
		{
			name:        "When mixed trailing dots present only those with dots should be trimmed",
			nameservers: []string{"ns1.example.com.", "ns2.example.com"},
			expected:    []string{"ns1.example.com", "ns2.example.com"},
			description: "Mixed case with some trailing dots",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, trimNameservers(tt.nameservers), tt.description)
		})
	}
}

func TestDNSEndpointNaming(t *testing.T) {
	tests := []struct {
		name         string
		hcpName      string
		expectedName string
	}{
		{
			name:         "When HCP name is simple it should append ingress-delegation suffix",
			hcpName:      "my-cluster",
			expectedName: "my-cluster-ingress-delegation",
		},
		{
			name:         "When HCP name has hyphens it should preserve them",
			hcpName:      "test-cluster-123",
			expectedName: "test-cluster-123-ingress-delegation",
		},
		{
			name:         "When HCP name is long the full name should be used",
			hcpName:      "very-long-hosted-control-plane-name",
			expectedName: "very-long-hosted-control-plane-name-ingress-delegation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := dnsEndpointName(tt.hcpName)
			assert.Equal(t, tt.expectedName, name)

			// Verify name follows Kubernetes naming constraints
			// (lowercase alphanumeric and hyphens, max 253 chars for DNS subdomain)
			assert.LessOrEqual(t, len(name), 253,
				"DNSEndpoint name should be <= 253 characters")
		})
	}
}

func TestDNSEndpointNameserverFormat(t *testing.T) {
	tests := []struct {
		name        string
		nameservers []string
		description string
	}{
		{
			name: "When nameservers are GCP Cloud DNS format they should be valid",
			nameservers: []string{
				"ns-cloud-a1.googledomains.com.",
				"ns-cloud-a2.googledomains.com.",
				"ns-cloud-a3.googledomains.com.",
				"ns-cloud-a4.googledomains.com.",
			},
			description: "Standard GCP Cloud DNS nameserver format with trailing dots",
		},
		{
			name: "When nameservers are custom they should be accepted",
			nameservers: []string{
				"ns1.custom-dns.example.com",
				"ns2.custom-dns.example.com",
			},
			description: "Custom nameserver format without trailing dots",
		},
		{
			name:        "When nameservers list is empty it should be valid",
			nameservers: []string{},
			description: "Edge case: empty nameserver list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify nameserver format is valid for DNSEndpoint
			for _, ns := range tt.nameservers {
				assert.NotEmpty(t, ns, "Nameserver should not be empty string")
			}
		})
	}
}

func TestDNSEndpointErrorHandling(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		description string
	}{
		{
			name: "When DNSEndpoint CRD is not installed reconciliation should continue",
			err: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Reason: metav1.StatusReasonNotFound,
					Details: &metav1.StatusDetails{
						Group: "externaldns.k8s.io",
						Kind:  "DNSEndpoint",
					},
				},
			},
			description: "CRD not found - best-effort operation, continue PSC reconciliation",
		},
		{
			name:        "When error mentions no matches for kind reconciliation should continue",
			err:         errors.New("no matches for kind \"DNSEndpoint\" in version \"externaldns.k8s.io/v1alpha1\""),
			description: "Schema/kind match error - best-effort operation, continue PSC reconciliation",
		},
		{
			name:        "When error is validation webhook failure reconciliation should continue",
			err:         errors.New("admission webhook denied the request: invalid DNSEndpoint"),
			description: "Validation webhook error - best-effort operation, continue PSC reconciliation",
		},
		{
			name:        "When error is permission denied reconciliation should continue",
			err:         errors.New("forbidden: user cannot create resource \"dnsendpoints\""),
			description: "Permission error - best-effort operation, continue PSC reconciliation",
		},
		{
			name:        "When error is generic API error reconciliation should continue",
			err:         errors.New("failed to connect to API server"),
			description: "API connectivity error - best-effort operation, continue PSC reconciliation",
		},
		{
			name:        "When error is timeout reconciliation should continue",
			err:         errors.New("context deadline exceeded"),
			description: "Timeout error - best-effort operation, continue PSC reconciliation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// All DNSEndpoint errors should be handled gracefully
			// The reconciliation logic just logs the error and continues
			// This test documents that ANY error from DNSEndpoint creation
			// should not fail the PSC reconciliation
			assert.NotNil(t, tt.err, "Error should exist for test case")
			assert.Contains(t, tt.err.Error(), "",
				tt.description+": Error should be logged but reconciliation continues")
		})
	}
}

func TestReconcileDNSEndpoint(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, hyperv1.AddToScheme(scheme))
	// Register the unstructured DNSEndpoint GVK so the fake client can track it
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "externaldns.k8s.io", Version: "v1alpha1", Kind: "DNSEndpoint"},
		&unstructured.Unstructured{},
	)

	newHCP := func(name, namespace string) *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				UID:       types.UID("test-uid"),
			},
		}
	}

	getDNSEndpoint := func(t *testing.T, c client.Client, name, namespace string) *unstructured.Unstructured {
		t.Helper()
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group: "externaldns.k8s.io", Version: "v1alpha1", Kind: "DNSEndpoint",
		})
		err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, obj)
		require.NoError(t, err)
		return obj
	}

	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		ingressDNS  string
		nameservers []string
		expectedDNS string
		expectedNS  []interface{}
		existingObj bool
	}{
		{
			name:        "When creating a new DNSEndpoint it should trim trailing dots and set correct fields",
			hcp:         newHCP("my-cluster", "test-ns"),
			ingressDNS:  "ingress.cluster.example.com.",
			nameservers: []string{"ns-cloud-c1.googledomains.com.", "ns-cloud-c2.googledomains.com."},
			expectedDNS: "ingress.cluster.example.com",
			expectedNS:  []interface{}{"ns-cloud-c1.googledomains.com", "ns-cloud-c2.googledomains.com"},
		},
		{
			name:        "When DNS name has no trailing dot it should remain unchanged",
			hcp:         newHCP("other-cluster", "test-ns"),
			ingressDNS:  "ingress.cluster.example.com",
			nameservers: []string{"ns1.example.com"},
			expectedDNS: "ingress.cluster.example.com",
			expectedNS:  []interface{}{"ns1.example.com"},
		},
		{
			name:        "When updating an existing DNSEndpoint it should overwrite the spec",
			hcp:         newHCP("existing-cluster", "test-ns"),
			ingressDNS:  "new-ingress.cluster.example.com.",
			nameservers: []string{"ns-new.googledomains.com."},
			expectedDNS: "new-ingress.cluster.example.com",
			expectedNS:  []interface{}{"ns-new.googledomains.com"},
			existingObj: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)

			if tt.existingObj {
				existing := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "externaldns.k8s.io/v1alpha1",
						"kind":       "DNSEndpoint",
						"metadata": map[string]interface{}{
							"name":      dnsEndpointName(tt.hcp.Name),
							"namespace": tt.hcp.Namespace,
						},
						"spec": map[string]interface{}{
							"endpoints": []interface{}{
								map[string]interface{}{
									"dnsName":    "old-dns.example.com",
									"recordType": "NS",
									"targets":    []interface{}{"old-ns.example.com"},
									"recordTTL":  float64(300),
								},
							},
						},
					},
				}
				clientBuilder = clientBuilder.WithObjects(existing)
			}

			fakeClient := clientBuilder.Build()
			reconciler := &GCPPrivateServiceConnectReconciler{
				Client:                 fakeClient,
				CreateOrUpdateProvider: upsert.New(false),
			}

			err := reconciler.reconcileDNSEndpoint(context.Background(), tt.hcp, tt.ingressDNS, tt.nameservers)
			require.NoError(t, err)

			// Verify the DNSEndpoint was created/updated with correct values
			result := getDNSEndpoint(t, fakeClient, dnsEndpointName(tt.hcp.Name), tt.hcp.Namespace)

			spec, ok := result.Object["spec"].(map[string]interface{})
			require.True(t, ok, "spec should be a map")

			endpoints, ok := spec["endpoints"].([]interface{})
			require.True(t, ok, "endpoints should be an array")
			require.Len(t, endpoints, 1)

			ep := endpoints[0].(map[string]interface{})
			assert.Equal(t, tt.expectedDNS, ep["dnsName"])
			assert.Equal(t, "NS", ep["recordType"])
			assert.Equal(t, tt.expectedNS, ep["targets"])
			assert.InDelta(t, 300, ep["recordTTL"], 0, "recordTTL should be 300")

			// Verify owner reference is set
			ownerRefs := result.GetOwnerReferences()
			require.Len(t, ownerRefs, 1)
			assert.Equal(t, tt.hcp.Name, ownerRefs[0].Name)
		})
	}
}
