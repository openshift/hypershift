package gcpprivateserviceconnect

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
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
