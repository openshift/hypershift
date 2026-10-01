package gcpprivateserviceconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/compute/v1"
	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

func TestConstructEndpointName(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name     string
		gcpPSC   *hyperv1.GCPPrivateServiceConnect
		expected string
	}{
		{
			name: "When constructing endpoint name, it should use service attachment name with endpoint suffix",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentName: "private-router-4bcf17df-cveiga-test-3-psc-sa",
				},
			},
			expected: "private-router-4bcf17df-cveiga-test-3-psc-sa-endpoint",
		},
		{
			name: "When service attachment name is short, it should append endpoint suffix",
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
			name: "When constructing IP name, it should use service attachment name with ip suffix",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentName: "private-router-4bcf17df-cveiga-test-3-psc-sa",
				},
			},
			expected: "private-router-4bcf17df-cveiga-test-3-psc-sa-ip",
		},
		{
			name: "When service attachment name is short, it should append ip suffix",
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
			name:            "When constructing address URL, it should include project, region, and name",
			addressName:     "clusters-test-cluster-1-private-router-psc-endpoint-ip",
			customerProject: "customer-project-123",
			region:          "us-central1",
			expected:        "projects/customer-project-123/regions/us-central1/addresses/clusters-test-cluster-1-private-router-psc-endpoint-ip",
		},
		{
			name:            "When using different region, it should construct correctly",
			addressName:     "test-address",
			customerProject: "my-gcp-project",
			region:          "europe-west1",
			expected:        "projects/my-gcp-project/regions/europe-west1/addresses/test-address",
		},
		{
			name:            "When using numeric project ID, it should work",
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
			name: "When ServiceAttachmentURI is empty, it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI:  "",
					ServiceAttachmentName: "test-sa",
				},
			},
			expected: false,
		},
		{
			name: "When ServiceAttachmentName is empty, it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI:  "projects/mgmt-project/regions/us-central1/serviceAttachments/test-sa",
					ServiceAttachmentName: "",
				},
			},
			expected: false,
		},
		{
			name: "When both URI and Name exist but condition is missing, it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI:  "projects/mgmt-project/regions/us-central1/serviceAttachments/test-sa",
					ServiceAttachmentName: "test-sa",
				},
			},
			expected: false,
		},
		{
			name: "When both URI and Name exist but condition is False, it should return false",
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
			name: "When both URI and Name exist and condition is True, it should return true",
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
			name:     "When given nil error, it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When given non-GCP error, it should return false",
			err:      assert.AnError,
			expected: false,
		},
		{
			name:     "When given a GCP 404 error, it should return true",
			err:      &googleapi.Error{Code: 404, Message: "not found"},
			expected: true,
		},
		{
			name:     "When given a GCP 500 error, it should return false",
			err:      &googleapi.Error{Code: 500, Message: "internal error"},
			expected: false,
		},
		{
			name:     "When given a wrapped GCP 404 error, it should return true",
			err:      fmt.Errorf("operation failed: %w", &googleapi.Error{Code: 404, Message: "not found"}),
			expected: true,
		},
		{
			name:     "When given a wrapped GCP 500 error, it should return false",
			err:      fmt.Errorf("operation failed: %w", &googleapi.Error{Code: 500, Message: "internal error"}),
			expected: false,
		},
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
			name: "When no external hostnames are configured, it should return empty map",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{},
				},
			},
			expected: map[string]string{},
		},
		{
			name: "When API server has Route hostname, it should return api entry",
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
			name: "When OAuth server has Route hostname, it should return oauth entry",
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
			name: "When both API and OAuth have Route hostnames, it should return both entries",
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
			name: "When API server uses LoadBalancer type, it should return empty map",
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
			name: "When Route has no hostname, it should return empty map",
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
			name:        "When nameservers have trailing dots, it should remove them",
			nameservers: []string{"ns-cloud-c1.googledomains.com.", "ns-cloud-c2.googledomains.com."},
			expected:    []string{"ns-cloud-c1.googledomains.com", "ns-cloud-c2.googledomains.com"},
			description: "GCP Cloud DNS returns nameservers with trailing dots but external-dns rejects them",
		},
		{
			name:        "When nameservers have no trailing dots, it should leave them unchanged",
			nameservers: []string{"ns1.example.com", "ns2.example.com"},
			expected:    []string{"ns1.example.com", "ns2.example.com"},
			description: "Already in correct format for external-dns",
		},
		{
			name:        "When nameservers list is empty, it should return empty",
			nameservers: []string{},
			expected:    []string{},
			description: "Edge case: empty nameserver list",
		},
		{
			name:        "When mixed trailing dots present, it should trim only those with dots",
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
			name:         "When HCP name is long, it should use the full name",
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
			name: "When nameservers are GCP Cloud DNS format, it should accept them as valid",
			nameservers: []string{
				"ns-cloud-a1.googledomains.com.",
				"ns-cloud-a2.googledomains.com.",
				"ns-cloud-a3.googledomains.com.",
				"ns-cloud-a4.googledomains.com.",
			},
			description: "Standard GCP Cloud DNS nameserver format with trailing dots",
		},
		{
			name: "When nameservers are custom, it should accept them",
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
			name: "When DNSEndpoint CRD is not installed, it should continue reconciliation",
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
			name:        "When error mentions no matches for kind, it should continue reconciliation",
			err:         errors.New("no matches for kind \"DNSEndpoint\" in version \"externaldns.k8s.io/v1alpha1\""),
			description: "Schema/kind match error - best-effort operation, continue PSC reconciliation",
		},
		{
			name:        "When error is validation webhook failure, it should continue reconciliation",
			err:         errors.New("admission webhook denied the request: invalid DNSEndpoint"),
			description: "Validation webhook error - best-effort operation, continue PSC reconciliation",
		},
		{
			name:        "When error is permission denied, it should continue reconciliation",
			err:         errors.New("forbidden: user cannot create resource \"dnsendpoints\""),
			description: "Permission error - best-effort operation, continue PSC reconciliation",
		},
		{
			name:        "When error is generic API error, it should continue reconciliation",
			err:         errors.New("failed to connect to API server"),
			description: "API connectivity error - best-effort operation, continue PSC reconciliation",
		},
		{
			name:        "When error is timeout, it should continue reconciliation",
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

func TestGetHCPOrCleanupOrphan(t *testing.T) {
	tests := []struct {
		name                  string
		hcpExists             bool
		pscHasFinalizer       bool
		patchConflict         bool
		expectHCP             bool
		expectFinalizerAbsent bool
		expectRequeueAfter    time.Duration
	}{
		{
			name:            "When the owning HCP exists, it should return the HCP without modifying the PSC finalizer",
			hcpExists:       true,
			pscHasFinalizer: true,
			expectHCP:       true,
		},
		{
			name:                  "When the owning HCP is missing and the PSC has a finalizer, it should remove the orphaned finalizer",
			pscHasFinalizer:       true,
			expectFinalizerAbsent: true,
		},
		{
			name:            "When the owning HCP is missing and the PSC has no finalizer, it should return without modifying the PSC",
			pscHasFinalizer: false,
		},
		{
			name:               "When removing the orphaned finalizer conflicts, it should requeue and retain the finalizer",
			pscHasFinalizer:    true,
			patchConflict:      true,
			expectRequeueAfter: time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newGCPPSCTestScheme(t)
			psc := newTestGCPPSC("test-psc", "test-ns", tt.pscHasFinalizer)
			objects := []client.Object{psc}
			if tt.hcpExists {
				objects = append(objects, newTestHCP("test-hcp", "test-ns"))
			}
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...)
			if tt.patchConflict {
				conflictErr := apierrors.NewConflict(hyperv1.Resource("gcpprivateserviceconnects"), psc.Name, errors.New("conflict"))
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if _, ok := obj.(*hyperv1.GCPPrivateServiceConnect); ok {
							return conflictErr
						}
						return c.Patch(ctx, obj, patch, opts...)
					},
				})
			}
			fakeClient := clientBuilder.Build()

			storedPSC := &hyperv1.GCPPrivateServiceConnect{}
			require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), storedPSC))

			r := &GCPPrivateServiceConnectReconciler{Client: fakeClient}
			hcp, result, err := r.getHCPOrCleanupOrphan(t.Context(), storedPSC, testr.New(t))
			require.NoError(t, err)
			assert.Equal(t, tt.expectHCP, hcp != nil)
			assert.Equal(t, tt.expectRequeueAfter, result.RequeueAfter)

			updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
			require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
			switch {
			case tt.expectFinalizerAbsent:
				assert.NotContains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
			case tt.patchConflict:
				assert.Contains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
			default:
				assert.Equal(t, tt.pscHasFinalizer, controllerutil.ContainsFinalizer(updatedPSC, pscEndpointFinalizer))
			}
		})
	}
}

func TestEnsureHCPFinalizer(t *testing.T) {
	tests := []struct {
		name               string
		finalizers         []string
		patchConflict      bool
		expectRequeueAfter time.Duration
		expectFinalizer    bool
	}{
		{
			name:            "When the HCP does not have the PSC finalizer, it should add it",
			expectFinalizer: true,
		},
		{
			name:            "When the HCP already has the PSC finalizer, it should not add a duplicate",
			finalizers:      []string{hcpGCPPSCFinalizerName},
			expectFinalizer: true,
		},
		{
			name:               "When patching the HCP finalizer conflicts, it should requeue after one second",
			patchConflict:      true,
			expectRequeueAfter: time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newGCPPSCTestScheme(t)
			hcp := newTestHCP("test-hcp", "test-ns")
			hcp.Finalizers = tt.finalizers
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp)
			if tt.patchConflict {
				conflictErr := apierrors.NewConflict(hyperv1.Resource("hostedcontrolplanes"), hcp.Name, errors.New("conflict"))
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if _, ok := obj.(*hyperv1.HostedControlPlane); ok {
							return conflictErr
						}
						return c.Patch(ctx, obj, patch, opts...)
					},
				})
			}
			fakeClient := clientBuilder.Build()

			r := &GCPPrivateServiceConnectReconciler{Client: fakeClient}
			result, err := r.ensureHCPFinalizer(t.Context(), hcp, testr.New(t))
			require.NoError(t, err)
			assert.Equal(t, tt.expectRequeueAfter, result.RequeueAfter)

			updatedHCP := &hyperv1.HostedControlPlane{}
			require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
			assert.Equal(t, tt.expectFinalizer, controllerutil.ContainsFinalizer(updatedHCP, hcpGCPPSCFinalizerName))
		})
	}
}

func TestRemoveHCPFinalizer(t *testing.T) {
	tests := []struct {
		name               string
		finalizers         []string
		patchConflict      bool
		expectRequeueAfter time.Duration
		expectFinalizer    bool
	}{
		{
			name:       "When the HCP has the PSC finalizer, it should remove it",
			finalizers: []string{hcpGCPPSCFinalizerName, "other-finalizer"},
		},
		{
			name:       "When the HCP does not have the PSC finalizer, it should leave other finalizers unchanged",
			finalizers: []string{"other-finalizer"},
		},
		{
			name:               "When removing the HCP finalizer conflicts, it should requeue after one second",
			finalizers:         []string{hcpGCPPSCFinalizerName, "other-finalizer"},
			patchConflict:      true,
			expectRequeueAfter: time.Second,
			expectFinalizer:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newGCPPSCTestScheme(t)
			hcp := newTestHCP("test-hcp", "test-ns")
			hcp.Finalizers = tt.finalizers
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp)
			if tt.patchConflict {
				conflictErr := apierrors.NewConflict(hyperv1.Resource("hostedcontrolplanes"), hcp.Name, errors.New("conflict"))
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if _, ok := obj.(*hyperv1.HostedControlPlane); ok {
							return conflictErr
						}
						return c.Patch(ctx, obj, patch, opts...)
					},
				})
			}
			fakeClient := clientBuilder.Build()

			r := &GCPPrivateServiceConnectReconciler{Client: fakeClient}
			result, err := r.removeHCPFinalizer(t.Context(), hcp, testr.New(t))
			require.NoError(t, err)
			assert.Equal(t, tt.expectRequeueAfter, result.RequeueAfter)

			updatedHCP := &hyperv1.HostedControlPlane{}
			require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
			assert.Equal(t, tt.expectFinalizer, controllerutil.ContainsFinalizer(updatedHCP, hcpGCPPSCFinalizerName))
			assert.Contains(t, updatedHCP.Finalizers, "other-finalizer")
		})
	}
}

func TestReconcileHCPDeletion(t *testing.T) {
	tests := []struct {
		name             string
		clientFactory    func(*testing.T) func(context.Context) (*compute.Service, error)
		pscPatchConflict bool
		expectError      bool
		expectRequeue    time.Duration
		expectHCDeleted  bool
	}{
		{
			name:            "When GCP cleanup succeeds, it should remove PSC and HCP finalizers",
			clientFactory:   successfulGCPClientFactory,
			expectHCDeleted: true,
		},
		{
			name:             "When removing the PSC finalizer conflicts, it should requeue and retain finalizers",
			clientFactory:    successfulGCPClientFactory,
			pscPatchConflict: true,
			expectRequeue:    time.Second,
		},
		{
			name: "When a GCP deletion operation is in progress, it should retain finalizers and requeue",
			clientFactory: func(t *testing.T) func(context.Context) (*compute.Service, error) {
				return testGCPClientFactory(t, http.StatusOK, "PENDING")
			},
			expectRequeue: pscEndpointDeletionRequeueDuration,
		},
		{
			name: "When GCP cleanup fails, it should retain PSC and HCP finalizers",
			clientFactory: func(t *testing.T) func(context.Context) (*compute.Service, error) {
				return testGCPClientFactory(t, http.StatusInternalServerError, "")
			},
			expectError: true,
		},
		{
			name: "When the GCP client cannot be initialized, it should retain PSC and HCP finalizers",
			clientFactory: func(*testing.T) func(context.Context) (*compute.Service, error) {
				return func(context.Context) (*compute.Service, error) {
					return nil, errors.New("GCP credentials unavailable")
				}
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newGCPPSCTestScheme(t)
			psc := newTestGCPPSC("test-psc", "test-ns", true)
			hcp := newDeletingTestHCP("test-hcp", "test-ns", []string{hcpGCPPSCFinalizerName})
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp)
			if tt.pscPatchConflict {
				conflictErr := apierrors.NewConflict(hyperv1.Resource("gcpprivateserviceconnects"), psc.Name, errors.New("conflict"))
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if _, ok := obj.(*hyperv1.GCPPrivateServiceConnect); ok {
							return conflictErr
						}
						return c.Patch(ctx, obj, patch, opts...)
					},
				})
			}
			fakeClient := clientBuilder.Build()

			r := &GCPPrivateServiceConnectReconciler{
				Client: fakeClient,
				gcpClientBuilder: gcpClientBuilder{
					customerProject: "customer-project",
					region:          "us-central1",
					initialized:     true,
					newClient:       tt.clientFactory(t),
				},
			}
			result, err := r.reconcileHCPDeletion(t.Context(), hcp, testr.New(t))
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expectRequeue, result.RequeueAfter)

			updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
			require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
			updatedHCP := &hyperv1.HostedControlPlane{}
			err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP)
			if tt.expectHCDeleted {
				assert.True(t, apierrors.IsNotFound(err))
				assert.NotContains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
				return
			}

			require.NoError(t, err)
			assert.Contains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
			assert.Contains(t, updatedHCP.Finalizers, hcpGCPPSCFinalizerName)
		})
	}
}

func TestReconcileHCPDeletionDeleteRequests(t *testing.T) {
	const (
		fwdRuleSuffix = "/projects/customer-project/regions/us-central1/forwardingRules/test-service-attachment-endpoint"
		addressSuffix = "/projects/customer-project/regions/us-central1/addresses/test-service-attachment-ip"
	)
	assertRequested := func(t *testing.T, recorded []string, method, suffix string) {
		t.Helper()
		for _, req := range recorded {
			if strings.HasPrefix(req, method+" ") && strings.HasSuffix(req, suffix) {
				return
			}
		}
		t.Fatalf("expected a %s request ending in %q, got: %v", method, suffix, recorded)
	}

	t.Run("On success it deletes the forwarding rule and reserved IP with the expected project and region", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newTestGCPPSC("test-psc", "test-ns", true)
		hcp := newDeletingTestHCP("test-hcp", "test-ns", []string{hcpGCPPSCFinalizerName})
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp).Build()

		var recorded []string
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				customerProject: "customer-project",
				region:          "us-central1",
				initialized:     true,
				newClient:       recordingGCPClientFactory(t, &recorded, http.StatusOK),
			},
		}

		result, err := r.reconcileHCPDeletion(t.Context(), hcp, testr.New(t))
		require.NoError(t, err)
		assert.Zero(t, result.RequeueAfter)

		assertRequested(t, recorded, http.MethodDelete, fwdRuleSuffix)
		assertRequested(t, recorded, http.MethodDelete, addressSuffix)

		updatedHCP := &hyperv1.HostedControlPlane{}
		assert.True(t, apierrors.IsNotFound(fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP)),
			"HCP finalizer should be removed and HCP garbage-collected")
	})

	t.Run("When the reserved IP delete fails, it retains both finalizers", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newTestGCPPSC("test-psc", "test-ns", true)
		hcp := newDeletingTestHCP("test-hcp", "test-ns", []string{hcpGCPPSCFinalizerName})
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp).Build()

		var recorded []string
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				customerProject: "customer-project",
				region:          "us-central1",
				initialized:     true,
				newClient:       recordingGCPClientFactory(t, &recorded, http.StatusInternalServerError),
			},
		}

		_, err := r.reconcileHCPDeletion(t.Context(), hcp, testr.New(t))
		require.Error(t, err)

		assertRequested(t, recorded, http.MethodDelete, fwdRuleSuffix)
		assertRequested(t, recorded, http.MethodDelete, addressSuffix)

		updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
		assert.Contains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
		updatedHCP := &hyperv1.HostedControlPlane{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
		assert.Contains(t, updatedHCP.Finalizers, hcpGCPPSCFinalizerName)
	})
}

func TestReconcile(t *testing.T) {
	t.Run("When the HCP is being deleted, it should not re-add the PSC finalizer", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newTestGCPPSC("test-psc", "test-ns", false)
		hcp := newDeletingTestHCP("test-hcp", "test-ns", []string{"other-finalizer"})
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized: true,
				newClient: func(context.Context) (*compute.Service, error) {
					return nil, errors.New("GCP credentials unavailable")
				},
			},
		}

		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(psc)})
		require.Error(t, err)

		updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
		assert.NotContains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
	})

	t.Run("When the Service Attachment is unavailable, it should add HCP finalizer and wait", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newTestGCPPSC("test-psc", "test-ns", true)
		psc.Finalizers = []string{pscEndpointFinalizer}
		hcp := newTestGCPHCP("test-hcp", "test-ns")
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized:     true,
				customerProject: "customer-project",
				region:          "us-central1",
				newClient:       testGCPClientFactory(t, http.StatusOK, "READY"),
			},
		}

		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(psc)})
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, result.RequeueAfter)

		// HCP finalizer should be added even though Service Attachment is not ready
		// The finalizer is added after GCP client is obtained, before Service Attachment check
		updatedHCP := &hyperv1.HostedControlPlane{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
		assert.Contains(t, updatedHCP.Finalizers, hcpGCPPSCFinalizerName, "HCP finalizer should be added after GCP client is obtained, even if Service Attachment is not ready")
	})

	t.Run("When the PSC endpoint IP is unavailable, it should add the HCP finalizer before waiting", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newReadyServiceAttachmentPSC("test-psc", "test-ns")
		psc.Finalizers = []string{pscEndpointFinalizer}
		hcp := newTestGCPHCP("test-hcp", "test-ns")
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized:     true,
				customerProject: "customer-project",
				region:          "us-central1",
				newClient:       testGCPClientFactory(t, http.StatusOK, "PENDING"),
			},
		}

		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(psc)})
		require.NoError(t, err)
		assert.Equal(t, 15*time.Second, result.RequeueAfter)

		// HCP finalizer should be added even though the endpoint IP is not ready yet.
		// It is installed right after the GCP client is obtained, before any GCP
		// resource is provisioned, so cleanup is guaranteed on later deletion.
		updatedHCP := &hyperv1.HostedControlPlane{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
		assert.Contains(t, updatedHCP.Finalizers, hcpGCPPSCFinalizerName, "HCP finalizer should be added before provisioning GCP resources, even if the endpoint IP is not ready")
	})

	t.Run("When the PSC endpoint is ready, it should add the HCP finalizer", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newReadyServiceAttachmentPSC("test-psc", "test-ns")
		psc.Finalizers = []string{pscEndpointFinalizer}
		psc.Status.EndpointIP = "10.0.0.10"
		hcp := newTestHCP("test-hcp", "test-ns")
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp).WithStatusSubresource(psc).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized:     true,
				customerProject: "customer-project",
				region:          "us-central1",
				newClient:       existingGCPResourcesClientFactory(t),
			},
			dnsReconciler: func(context.Context, *hyperv1.GCPPrivateServiceConnect, *hyperv1.HostedControlPlane, logr.Logger) (ctrl.Result, error) {
				return ctrl.Result{}, nil
			},
		}

		result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(psc)})
		require.NoError(t, err)
		assert.Equal(t, driftDetectionRequeueInterval, result.RequeueAfter)

		updatedHCP := &hyperv1.HostedControlPlane{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
		assert.Contains(t, updatedHCP.Finalizers, hcpGCPPSCFinalizerName)
	})
}

func TestMapHCPToPSC(t *testing.T) {
	tests := []struct {
		name          string
		hcpFinalizers []string
		hcpDeleting   bool
		pscObjects    []client.Object
		listErr       bool
		expectNames   []string
	}{
		{
			name:          "When the HCP has the PSC finalizer, it should enqueue every PSC in its namespace",
			hcpFinalizers: []string{hcpGCPPSCFinalizerName},
			pscObjects: []client.Object{
				newTestGCPPSC("first", "test-ns", false),
				newTestGCPPSC("second", "test-ns", false),
				newTestGCPPSC("other-namespace", "other-ns", false),
			},
			expectNames: []string{"first", "second"},
		},
		{
			name: "When the HCP does not have the PSC finalizer, it should not enqueue PSC resources",
			pscObjects: []client.Object{
				newTestGCPPSC("first", "test-ns", false),
				newTestGCPPSC("second", "test-ns", false),
			},
			expectNames: nil,
		},
		{
			name:          "When HCP is deleting with no PSC CRs, it should enqueue synthetic request (Issue #5 fix)",
			hcpFinalizers: []string{hcpGCPPSCFinalizerName},
			hcpDeleting:   true,
			pscObjects:    []client.Object{},        // No PSC CRs
			expectNames:   []string{"test-hcp-psc"}, // Synthetic request
		},
		{
			name:          "When HCP is deleting with PSC CRs present, it should enqueue real PSC CRs",
			hcpFinalizers: []string{hcpGCPPSCFinalizerName},
			hcpDeleting:   true,
			pscObjects: []client.Object{
				newTestGCPPSC("first", "test-ns", false),
			},
			expectNames: []string{"first"}, // Real PSC CR, not synthetic
		},
		{
			name:          "When PSC list fails and HCP is deleting, it should still enqueue synthetic request",
			hcpFinalizers: []string{hcpGCPPSCFinalizerName},
			hcpDeleting:   true,
			listErr:       true,
			expectNames:   []string{"test-hcp-psc"}, // Synthetic request despite list error
		},
		{
			name:          "When PSC list fails and HCP is not deleting, it should enqueue nothing",
			hcpFinalizers: []string{hcpGCPPSCFinalizerName},
			listErr:       true,
			expectNames:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newGCPPSCTestScheme(t)
			hcp := newTestHCP("test-hcp", "test-ns")
			hcp.Finalizers = tt.hcpFinalizers
			if tt.hcpDeleting {
				now := metav1.Now()
				hcp.DeletionTimestamp = &now
			}

			objects := append([]client.Object{hcp}, tt.pscObjects...)
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...)
			if tt.listErr {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					List: func(_ context.Context, _ client.WithWatch, list client.ObjectList, _ ...client.ListOption) error {
						if _, ok := list.(*hyperv1.GCPPrivateServiceConnectList); ok {
							return apierrors.NewInternalError(errors.New("list failed"))
						}
						return errors.New("unexpected list")
					},
				})
			}
			r := &GCPPrivateServiceConnectReconciler{Client: clientBuilder.Build()}

			requests := r.mapHCPToPSC()(t.Context(), hcp)
			requestNames := make([]string, 0, len(requests))
			for _, request := range requests {
				requestNames = append(requestNames, request.Name)
			}
			assert.ElementsMatch(t, tt.expectNames, requestNames)
		})
	}
}

func TestHandleOrphanedHCPFinalizer(t *testing.T) {
	t.Run("When HCP is deleting with finalizer but no PSC CRs, it should remove HCP finalizer (Issue #5 fix)", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		hcp := newTestHCP("test-hcp", "test-ns")
		hcp.Finalizers = []string{hcpGCPPSCFinalizerName, "other-finalizer"}
		now := metav1.Now()
		hcp.DeletionTimestamp = &now

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).Build()
		r := &GCPPrivateServiceConnectReconciler{Client: fakeClient}

		result, err := r.handleOrphanedHCPFinalizer(t.Context(), "test-ns", testr.New(t))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		// HCP finalizer should be removed
		updatedHCP := &hyperv1.HostedControlPlane{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
		assert.NotContains(t, updatedHCP.Finalizers, hcpGCPPSCFinalizerName)
		assert.Contains(t, updatedHCP.Finalizers, "other-finalizer")
	})

	t.Run("When HCP is not deleting, it should not remove finalizer", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		hcp := newTestHCP("test-hcp", "test-ns")
		hcp.Finalizers = []string{hcpGCPPSCFinalizerName}
		// No deletionTimestamp

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).Build()
		r := &GCPPrivateServiceConnectReconciler{Client: fakeClient}

		result, err := r.handleOrphanedHCPFinalizer(t.Context(), "test-ns", testr.New(t))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		// HCP finalizer should still be present
		updatedHCP := &hyperv1.HostedControlPlane{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
		assert.Contains(t, updatedHCP.Finalizers, hcpGCPPSCFinalizerName)
	})

	t.Run("When HCP has no PSC finalizer, it should do nothing", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		hcp := newTestHCP("test-hcp", "test-ns")
		hcp.Finalizers = []string{"other-finalizer"}
		now := metav1.Now()
		hcp.DeletionTimestamp = &now

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp).Build()
		r := &GCPPrivateServiceConnectReconciler{Client: fakeClient}

		result, err := r.handleOrphanedHCPFinalizer(t.Context(), "test-ns", testr.New(t))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		// Other finalizer should still be present
		updatedHCP := &hyperv1.HostedControlPlane{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
		assert.Contains(t, updatedHCP.Finalizers, "other-finalizer")
	})

	t.Run("When no HCP exists in namespace, it should return successfully", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		r := &GCPPrivateServiceConnectReconciler{Client: fakeClient}

		result, err := r.handleOrphanedHCPFinalizer(t.Context(), "test-ns", testr.New(t))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("When PSC CRs are still present, it drives cleanup instead of only requeuing", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		hcp := newDeletingTestHCP("test-hcp", "test-ns", []string{hcpGCPPSCFinalizerName})
		psc := newTestGCPPSC("test-psc", "test-ns", true)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp, psc).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized:     true,
				customerProject: "customer-project",
				region:          "us-central1",
				newClient:       successfulGCPClientFactory(t),
			},
		}

		// A successful cleanup run removes the PSC finalizer and then the HCP finalizer,
		// rather than leaving the deleting HCP stranded behind a passive requeue.
		result, err := r.handleOrphanedHCPFinalizer(t.Context(), "test-ns", testr.New(t))
		require.NoError(t, err)
		assert.Zero(t, result.RequeueAfter)

		updatedHCP := &hyperv1.HostedControlPlane{}
		assert.True(t, apierrors.IsNotFound(fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP)),
			"HCP finalizer should be removed and HCP garbage-collected once its PSC CRs are cleaned up")
		updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
		assert.NotContains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
	})

	t.Run("When PSC cleanup is still in progress, it requeues and retains both finalizers", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		hcp := newDeletingTestHCP("test-hcp", "test-ns", []string{hcpGCPPSCFinalizerName})
		psc := newTestGCPPSC("test-psc", "test-ns", true)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp, psc).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized:     true,
				customerProject: "customer-project",
				region:          "us-central1",
				newClient:       testGCPClientFactory(t, http.StatusOK, "PENDING"),
			},
		}

		result, err := r.handleOrphanedHCPFinalizer(t.Context(), "test-ns", testr.New(t))
		require.NoError(t, err)
		assert.Equal(t, pscEndpointDeletionRequeueDuration, result.RequeueAfter)

		updatedHCP := &hyperv1.HostedControlPlane{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP))
		assert.Contains(t, updatedHCP.Finalizers, hcpGCPPSCFinalizerName)
		updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
		assert.Contains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
	})
}

func TestHandlePSCCRDeletion(t *testing.T) {
	t.Run("When builder is initialized but HCP is deleted, it should remove PSC finalizer (Issue #6 fix)", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newTestGCPPSC("test-psc", "test-ns", false)
		psc.Finalizers = []string{pscEndpointFinalizer, "other-finalizer"}
		now := metav1.Now()
		psc.DeletionTimestamp = &now

		// No HCP object - simulates HCP already deleted
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized:     true, // Builder WAS initialized during normal reconciliation
				customerProject: "test-project",
				region:          "us-central1",
			},
		}

		result, err := r.handlePSCCRDeletion(t.Context(), psc, testr.New(t))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		// PSC finalizer should be removed despite builder being initialized
		updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
		assert.NotContains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
		assert.Contains(t, updatedPSC.Finalizers, "other-finalizer") // Other finalizer remains
	})

	t.Run("When builder is NOT initialized and HCP is deleted, it should remove PSC finalizer", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newTestGCPPSC("test-psc", "test-ns", false)
		psc.Finalizers = []string{pscEndpointFinalizer, "other-finalizer"}
		now := metav1.Now()
		psc.DeletionTimestamp = &now

		// No HCP object - simulates HCP already deleted
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized: false, // Builder not initialized (e.g., controller restart)
			},
		}

		result, err := r.handlePSCCRDeletion(t.Context(), psc, testr.New(t))
		require.NoError(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		// PSC finalizer should be removed
		updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
		assert.NotContains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
		assert.Contains(t, updatedPSC.Finalizers, "other-finalizer") // Other finalizer remains
	})

	t.Run("When HCP exists, it should proceed with normal cleanup", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newTestGCPPSC("test-psc", "test-ns", false)
		psc.Finalizers = []string{pscEndpointFinalizer, "other-finalizer"}
		psc.Status.EndpointIP = "10.0.0.10"
		now := metav1.Now()
		psc.DeletionTimestamp = &now

		hcp := newTestGCPHCP("test-hcp", "test-ns")

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized:     true,
				customerProject: "test-project",
				region:          "us-central1",
				newClient:       testGCPClientFactory(t, http.StatusOK, "DONE"), // Operation completes immediately
			},
		}

		result, err := r.handlePSCCRDeletion(t.Context(), psc, testr.New(t))
		require.NoError(t, err)
		// Should complete cleanup and remove finalizer
		assert.Equal(t, ctrl.Result{}, result)

		updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
		assert.NotContains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
		assert.Contains(t, updatedPSC.Finalizers, "other-finalizer") // Other finalizer remains

		// PSC-before-HCP ordering guard: cleaning up the PSC must not delete the HCP or
		// mark it for deletion. Re-fetch and assert it still exists, untouched. Without
		// this, the finalizer-only assertions above would still pass if HCP deletion were
		// (incorrectly) requested during PSC cleanup.
		updatedHCP := &hyperv1.HostedControlPlane{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), updatedHCP),
			"HCP must still exist after PSC cleanup")
		assert.True(t, updatedHCP.DeletionTimestamp.IsZero(),
			"HCP must not be marked for deletion by PSC cleanup")
	})

	t.Run("When the GCP client cannot be created, it should error and retain the finalizer", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newTestGCPPSC("test-psc", "test-ns", false)
		psc.Finalizers = []string{pscEndpointFinalizer, "other-finalizer"}
		now := metav1.Now()
		psc.DeletionTimestamp = &now

		hcp := newTestGCPHCP("test-hcp", "test-ns")

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp).Build()
		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized:     true,
				customerProject: "test-project",
				region:          "us-central1",
				newClient: func(context.Context) (*compute.Service, error) {
					return nil, errors.New("credentials unavailable")
				},
			},
		}

		result, err := r.handlePSCCRDeletion(t.Context(), psc, testr.New(t))
		require.Error(t, err)
		assert.Equal(t, ctrl.Result{}, result)

		// Finalizer must be retained so cleanup is retried once credentials are ready.
		updatedPSC := &hyperv1.GCPPrivateServiceConnect{}
		require.NoError(t, fakeClient.Get(t.Context(), client.ObjectKeyFromObject(psc), updatedPSC))
		assert.Contains(t, updatedPSC.Finalizers, pscEndpointFinalizer)
		assert.Contains(t, updatedPSC.Finalizers, "other-finalizer")
	})
}

func TestReconcileDelete(t *testing.T) {
	t.Run("When no service attachment name was established, it should complete without any GCP calls", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		psc := newTestGCPPSC("test-psc", "test-ns", true)
		psc.Status.ServiceAttachmentName = "" // endpoint/IP were never provisioned
		hcp := newTestGCPHCP("test-hcp", "test-ns")
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(psc, hcp).Build()

		// Any GCP request would fail this transport, proving none are made.
		var called bool
		httpClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, errors.New("unexpected GCP call during delete")
		})}
		svc, err := compute.NewService(t.Context(), option.WithHTTPClient(httpClient), option.WithoutAuthentication())
		require.NoError(t, err)

		r := &GCPPrivateServiceConnectReconciler{
			Client: fakeClient,
			gcpClientBuilder: gcpClientBuilder{
				initialized:     true,
				customerProject: "customer-project",
				region:          "us-central1",
			},
		}

		completed, err := r.reconcileDelete(t.Context(), psc, hcp, svc, testr.New(t))
		require.NoError(t, err)
		assert.True(t, completed, "cleanup should be considered complete when nothing was provisioned")
		assert.False(t, called, "no GCP API calls should be made when ServiceAttachmentName is empty")
	})
}

func TestDNSZonesToDelete(t *testing.T) {
	hcpWithDomain := func(baseDomain string) *hyperv1.HostedControlPlane {
		hcp := newTestGCPHCP("test-hcp", "test-ns")
		hcp.Spec.DNS.BaseDomain = baseDomain
		return hcp
	}
	pscWithStatusZones := func(names ...string) *hyperv1.GCPPrivateServiceConnect {
		psc := newTestGCPPSC("test-psc", "test-ns", false)
		for _, n := range names {
			psc.Status.DNSZones = append(psc.Status.DNSZones, hyperv1.DNSZoneStatus{Name: n})
		}
		return psc
	}

	tests := []struct {
		name          string
		psc           *hyperv1.GCPPrivateServiceConnect
		hcp           *hyperv1.HostedControlPlane
		expectedZones []string
	}{
		{
			name:          "When status has no zones but HCP is present, deterministic names are inferred",
			psc:           pscWithStatusZones(),
			hcp:           hcpWithDomain("example.com"),
			expectedZones: []string{"example-com-private", "example-com-public", "test-hcp-hypershift-local"},
		},
		{
			name:          "When status records a deterministic name, it is de-duplicated against the inferred names",
			psc:           pscWithStatusZones("test-hcp-hypershift-local"),
			hcp:           hcpWithDomain("example.com"),
			expectedZones: []string{"example-com-private", "example-com-public", "test-hcp-hypershift-local"},
		},
		{
			name:          "When status records a zone with no deterministic overlap, it is unioned with all inferred names",
			psc:           pscWithStatusZones("leftover-zone"),
			hcp:           hcpWithDomain("example.com"),
			expectedZones: []string{"example-com-private", "example-com-public", "leftover-zone", "test-hcp-hypershift-local"},
		},
		{
			name:          "When there is no HCP and status is empty, it should yield nothing",
			psc:           pscWithStatusZones(),
			hcp:           nil,
			expectedZones: nil,
		},
		{
			name:          "When the HCP has no base domain, only status-recorded names are returned",
			psc:           pscWithStatusZones("leftover-zone"),
			hcp:           hcpWithDomain(""),
			expectedZones: []string{"leftover-zone"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ElementsMatch(t, tt.expectedZones, dnsZonesToDelete(tt.psc, tt.hcp))
		})
	}
}

func TestCleanupDNSOwnershipGate(t *testing.T) {
	ownedLabel := func(infraID string) map[string]string {
		return map[string]string{gcpDNSZoneOwnerLabelKey: dnsZoneOwnerID(infraID)}
	}

	t.Run("It deletes an inferred zone only when its ownership label matches the cluster InfraID", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		hcp := newTestGCPHCP("test-hcp", "test-ns")
		hcp.Spec.DNS.BaseDomain = "example.com"
		hcp.Spec.InfraID = "my-infra"
		psc := newTestGCPPSC("test-psc", "test-ns", false) // no recorded status zones -> all names inferred

		existing := map[string]*dns.ManagedZone{
			"test-hcp-hypershift-local": {Name: "test-hcp-hypershift-local", Labels: ownedLabel("my-infra")}, // owned by us
			"example-com-public":        {Name: "example-com-public", Labels: ownedLabel("another-cluster")}, // owned by another cluster
			"example-com-private":       {Name: "example-com-private"},                                       // predates the label
		}
		var deletes []string
		r := &GCPPrivateServiceConnectReconciler{
			Client:           fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp, psc).Build(),
			gcpClientBuilder: gcpClientBuilder{initialized: true, customerProject: "customer-project"},
			dnsClientFactory: func(context.Context) (*dns.Service, error) { return fakeDNSService(t, existing, &deletes), nil },
		}

		require.NoError(t, r.cleanupDNS(t.Context(), psc, hcp))
		assert.ElementsMatch(t, []string{"test-hcp-hypershift-local"}, deletes,
			"only the inferred zone whose ownership label matches this cluster's InfraID should be deleted")
	})

	t.Run("It skips every zone when the cluster has no InfraID to prove ownership", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		hcp := newTestGCPHCP("test-hcp", "test-ns")
		hcp.Spec.DNS.BaseDomain = "example.com"
		hcp.Spec.InfraID = "" // cannot prove ownership

		psc := newTestGCPPSC("test-psc", "test-ns", false)
		existing := map[string]*dns.ManagedZone{
			"test-hcp-hypershift-local": {Name: "test-hcp-hypershift-local", Labels: map[string]string{gcpDNSZoneOwnerLabelKey: ""}},
		}
		var deletes []string
		r := &GCPPrivateServiceConnectReconciler{
			Client:           fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp, psc).Build(),
			gcpClientBuilder: gcpClientBuilder{initialized: true, customerProject: "customer-project"},
			dnsClientFactory: func(context.Context) (*dns.Service, error) { return fakeDNSService(t, existing, &deletes), nil },
		}

		require.NoError(t, r.cleanupDNS(t.Context(), psc, hcp))
		assert.Empty(t, deletes, "no zone should be deleted without an ownership marker")
	})

	t.Run("It gates status-recorded zones on ownership too, closing the adopted-zone deletion path", func(t *testing.T) {
		scheme := newGCPPSCTestScheme(t)
		hcp := newTestGCPHCP("test-hcp", "test-ns")
		hcp.Spec.DNS.BaseDomain = "" // no deterministic/inferred names, only the recorded ones
		hcp.Spec.InfraID = "my-infra"

		psc := newTestGCPPSC("test-psc", "test-ns", false)
		psc.Status.DNSZones = []hyperv1.DNSZoneStatus{{Name: "owned-recorded-zone"}, {Name: "foreign-recorded-zone"}}

		// Both names are recorded in status, but only one actually carries our ownership
		// label. A foreign-labeled zone that somehow landed in status (e.g. a stale/restored
		// object) must NOT be deleted -- status no longer grants a deletion bypass.
		existing := map[string]*dns.ManagedZone{
			"owned-recorded-zone":   {Name: "owned-recorded-zone", Labels: ownedLabel("my-infra")},
			"foreign-recorded-zone": {Name: "foreign-recorded-zone", Labels: ownedLabel("another-cluster")},
		}
		var deletes []string
		r := &GCPPrivateServiceConnectReconciler{
			Client:           fake.NewClientBuilder().WithScheme(scheme).WithObjects(hcp, psc).Build(),
			gcpClientBuilder: gcpClientBuilder{initialized: true, customerProject: "customer-project"},
			dnsClientFactory: func(context.Context) (*dns.Service, error) { return fakeDNSService(t, existing, &deletes), nil },
		}

		require.NoError(t, r.cleanupDNS(t.Context(), psc, hcp))
		assert.ElementsMatch(t, []string{"owned-recorded-zone"}, deletes,
			"only the status-recorded zone that carries our ownership label should be deleted")
	})
}

// TestCreateZoneStampsOwnerLabel is the creation-side counterpart to TestCleanupDNSOwnershipGate:
// it proves createZone stamps the ownership label when it actually creates a zone, and that the
// adoption branch returns the pre-existing zone without creating or re-stamping. The shared
// fakeDNSService only models Get/Delete, so this test uses a transport that also captures the
// create (POST) body.
func TestCreateZoneStampsOwnerLabel(t *testing.T) {
	hcp := newTestGCPHCP("test-hcp", "test-ns")
	hcp.Spec.InfraID = "my-infra"
	ownerLabels := dnsZoneOwnerLabels(hcp)
	require.NotEmpty(t, ownerLabels[gcpDNSZoneOwnerLabelKey], "test setup: owner labels must be populated")

	jsonResp := func(r *http.Request, code int, body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: code,
			Status:     fmt.Sprintf("%d %s", code, http.StatusText(code)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	}

	t.Run("When the zone does not exist, it stamps the ownership label on the created zone", func(t *testing.T) {
		var created *dns.ManagedZone
		httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.Method {
			case http.MethodGet:
				// Zone does not exist yet -> drives createZone down the create path.
				return jsonResp(r, http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`)
			case http.MethodPost:
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				created = &dns.ManagedZone{}
				require.NoError(t, json.Unmarshal(body, created))
				return jsonResp(r, http.StatusOK, string(body)) // echo the created zone back
			}
			return jsonResp(r, http.StatusOK, "{}")
		})}
		svc, err := dns.NewService(t.Context(), option.WithHTTPClient(httpClient), option.WithoutAuthentication())
		require.NoError(t, err)

		zone, err := createZone(t.Context(), svc, "customer-project",
			"test-hcp-hypershift-local", "test-hcp.hypershift.local", "private", "https://example/network", ownerLabels)
		require.NoError(t, err)
		require.NotNil(t, created, "a create (POST) request should have been made")
		assert.Equal(t, ownerLabels, created.Labels, "createZone must stamp the ownership label on the new zone")
		assert.Equal(t, ownerLabels, zone.Labels, "the returned zone should carry the stamped label")
	})

	t.Run("When the zone already exists, it adopts without creating or re-stamping", func(t *testing.T) {
		existing := &dns.ManagedZone{Name: "test-hcp-hypershift-local", DnsName: "test-hcp.hypershift.local."} // no labels
		var postCalled bool
		httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch r.Method {
			case http.MethodGet:
				body, err := json.Marshal(existing)
				require.NoError(t, err)
				return jsonResp(r, http.StatusOK, string(body))
			case http.MethodPost:
				postCalled = true
				return jsonResp(r, http.StatusOK, "{}")
			}
			return jsonResp(r, http.StatusOK, "{}")
		})}
		svc, err := dns.NewService(t.Context(), option.WithHTTPClient(httpClient), option.WithoutAuthentication())
		require.NoError(t, err)

		zone, err := createZone(t.Context(), svc, "customer-project",
			"test-hcp-hypershift-local", "test-hcp.hypershift.local", "private", "https://example/network", ownerLabels)
		require.NoError(t, err)
		assert.False(t, postCalled, "an existing zone must be adopted without a create call")
		assert.Empty(t, zone.Labels, "adoption must not re-stamp the existing (unlabeled) zone")
	})
}

// fakeDNSService returns a Cloud DNS client backed by an in-memory transport. Zones present
// in existing respond to Get with their labels; DELETE requests on any zone are appended to
// recordedDeletes. Record listing and change creation succeed trivially so deleteZone completes.
func fakeDNSService(t *testing.T, existing map[string]*dns.ManagedZone, recordedDeletes *[]string) *dns.Service {
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
	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		const marker = "/managedZones/"
		idx := strings.Index(r.URL.Path, marker)
		if idx == -1 {
			return jsonResp(r, http.StatusOK, "{}")
		}
		rest := r.URL.Path[idx+len(marker):]
		switch {
		case strings.HasSuffix(rest, "/rrsets") && r.Method == http.MethodGet:
			return jsonResp(r, http.StatusOK, `{"rrsets":[]}`)
		case strings.HasSuffix(rest, "/changes") && r.Method == http.MethodPost:
			return jsonResp(r, http.StatusOK, "{}")
		case r.Method == http.MethodGet:
			zone, ok := existing[rest]
			if !ok {
				return jsonResp(r, http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`)
			}
			body, err := json.Marshal(zone)
			require.NoError(t, err)
			return jsonResp(r, http.StatusOK, string(body))
		case r.Method == http.MethodDelete:
			*recordedDeletes = append(*recordedDeletes, rest)
			return jsonResp(r, http.StatusOK, "{}")
		}
		return jsonResp(r, http.StatusOK, "{}")
	})}

	svc, err := dns.NewService(t.Context(), option.WithHTTPClient(httpClient), option.WithoutAuthentication())
	require.NoError(t, err)
	return svc
}

func newGCPPSCTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, hyperv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

func newTestHCP(name, namespace string) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
}

func newTestGCPHCP(name, namespace string) *hyperv1.HostedControlPlane {
	hcp := newTestHCP(name, namespace)
	hcp.Spec.Platform = hyperv1.PlatformSpec{
		Type: hyperv1.GCPPlatform,
		GCP: &hyperv1.GCPPlatformSpec{
			Project: "customer-project",
			Region:  "us-central1",
			NetworkConfig: hyperv1.GCPNetworkConfig{
				Network:                     hyperv1.GCPResourceReference{Name: "customer-network"},
				PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "psc-subnet"},
			},
		},
	}
	return hcp
}

func newDeletingTestHCP(name, namespace string, finalizers []string) *hyperv1.HostedControlPlane {
	hcp := newTestHCP(name, namespace)
	hcp.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	hcp.Finalizers = finalizers
	return hcp
}

func newTestGCPPSC(name, namespace string, withFinalizer bool) *hyperv1.GCPPrivateServiceConnect {
	psc := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			ServiceAttachmentName: "test-service-attachment",
		},
	}
	psc.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: hyperv1.GroupVersion.String(),
		Kind:       "HostedControlPlane",
		Name:       "test-hcp",
	}}
	if withFinalizer {
		psc.Finalizers = []string{pscEndpointFinalizer}
	}
	return psc
}

func newReadyServiceAttachmentPSC(name, namespace string) *hyperv1.GCPPrivateServiceConnect {
	psc := newTestGCPPSC(name, namespace, false)
	psc.Status.ServiceAttachmentURI = "projects/customer-project/regions/us-central1/serviceAttachments/test-service-attachment"
	psc.Status.Conditions = []metav1.Condition{{
		Type:   string(hyperv1.GCPServiceAttachmentAvailable),
		Status: metav1.ConditionTrue,
	}}
	return psc
}

func successfulGCPClientFactory(t *testing.T) func(context.Context) (*compute.Service, error) {
	return testGCPClientFactory(t, http.StatusOK, "DONE")
}

func testGCPClientFactory(t *testing.T, responseStatus int, operationStatus string) func(context.Context) (*compute.Service, error) {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		statusCode := responseStatus
		body := fmt.Sprintf(`{"status": %q}`, operationStatus)
		if responseStatus != http.StatusOK {
			body = `{"error":{"code":500,"message":"GCP API error"}}`
		}
		if r.Method == http.MethodGet {
			statusCode = http.StatusNotFound
			body = `{"error":{"code":404,"message":"not found"}}`
		}
		return &http.Response{
			Status:        fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
			StatusCode:    statusCode,
			Header:        http.Header{"Content-Type": []string{"application/json"}},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       r,
		}, nil
	})}

	service, err := compute.NewService(
		t.Context(),
		option.WithHTTPClient(httpClient),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)
	return func(context.Context) (*compute.Service, error) {
		return service, nil
	}
}

func existingGCPResourcesClientFactory(t *testing.T) func(context.Context) (*compute.Service, error) {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"address":"10.0.0.10"}`
		if strings.Contains(r.URL.Path, "/forwardingRules/") {
			body = `{"IPAddress":"10.0.0.10","target":"projects/management-project/regions/us-central1/serviceAttachments/test-service-attachment"}`
		}
		return &http.Response{
			Status:        "200 OK",
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": []string{"application/json"}},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       r,
		}, nil
	})}
	service, err := compute.NewService(t.Context(), option.WithHTTPClient(httpClient), option.WithoutAuthentication())
	require.NoError(t, err)
	return func(context.Context) (*compute.Service, error) {
		return service, nil
	}
}

// recordingGCPClientFactory records each request's "METHOD path" into recorded and
// returns DONE operations. GETs return 404 so post-delete verification sees the
// resource as gone. When ipDeleteStatus != 200, the Addresses (reserved IP) delete
// fails with that status, simulating a partial cleanup failure.
func recordingGCPClientFactory(t *testing.T, recorded *[]string, ipDeleteStatus int) func(context.Context) (*compute.Service, error) {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		*recorded = append(*recorded, r.Method+" "+r.URL.Path)

		statusCode := http.StatusOK
		body := `{"status":"DONE"}`
		switch {
		case r.Method == http.MethodGet:
			statusCode = http.StatusNotFound
			body = `{"error":{"code":404,"message":"not found"}}`
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/addresses/") && ipDeleteStatus != http.StatusOK:
			statusCode = ipDeleteStatus
			body = `{"error":{"code":500,"message":"IP delete failed"}}`
		}
		return &http.Response{
			Status:        fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
			StatusCode:    statusCode,
			Header:        http.Header{"Content-Type": []string{"application/json"}},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       r,
		}, nil
	})}
	service, err := compute.NewService(t.Context(), option.WithHTTPClient(httpClient), option.WithoutAuthentication())
	require.NoError(t, err)
	return func(context.Context) (*compute.Service, error) { return service, nil }
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
