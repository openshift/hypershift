package gcpprivateserviceconnect

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/dns/v1"
)

// realClusterID is a cluster ID in the form the HostedCluster controller generates.
const realClusterID = "3f2d5a6c-9b41-4e7a-8c0d-1e5f7a2b9c34"

func hcpWithClusterID(clusterID string) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{ClusterID: clusterID},
	}
}

func TestClusterOwnerID(t *testing.T) {
	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expected    string
		expectError bool
	}{
		{
			name:     "When clusterID is a lowercase UUID, it should return it unchanged",
			hcp:      hcpWithClusterID(realClusterID),
			expected: realClusterID,
		},
		{
			name:     "When clusterID contains uppercase hexadecimal, it should normalize it to lowercase",
			hcp:      hcpWithClusterID("3F2D5A6C-9B41-4E7A-8C0D-1E5F7A2B9C34"),
			expected: realClusterID,
		},
		{
			name:     "When clusterID begins with a digit, it should return it unchanged",
			hcp:      hcpWithClusterID("0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"),
			expected: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d",
		},
		{
			name:        "When clusterID is empty, it should return an error",
			hcp:         hcpWithClusterID(""),
			expectError: true,
		},
		{
			name:        "When clusterID is not a UUID, it should return an error",
			hcp:         hcpWithClusterID("not-a-uuid"),
			expectError: true,
		},
		{
			name:        "When clusterID is a truncated UUID, it should return an error",
			hcp:         hcpWithClusterID("3f2d5a6c-9b41-4e7a-8c0d"),
			expectError: true,
		},
		{
			name:        "When the HostedControlPlane is nil, it should return an error",
			hcp:         nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, err := clusterOwnerID(tt.hcp)
			if tt.expectError {
				require.Error(t, err)
				assert.Empty(t, owner, "no owner should be returned alongside an error")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, owner)
		})
	}
}

func TestClassifyZoneOwnership(t *testing.T) {
	tests := []struct {
		name     string
		zone     *dns.ManagedZone
		ownerID  string
		expected zoneOwnership
	}{
		{
			name:     "When the zone carries this cluster's ownership label, it should report owned by cluster",
			zone:     &dns.ManagedZone{Labels: map[string]string{ownershipLabelKey: realClusterID}},
			ownerID:  realClusterID,
			expected: zoneOwnedByCluster,
		},
		{
			name:     "When the zone carries another cluster's ownership label, it should report owned by other",
			zone:     &dns.ManagedZone{Labels: map[string]string{ownershipLabelKey: "8c7d6e5f-4a3b-4c2d-9e1f-0a1b2c3d4e5f"}},
			ownerID:  realClusterID,
			expected: zoneOwnedByOther,
		},
		{
			name:     "When the zone has no labels, it should report unlabeled",
			zone:     &dns.ManagedZone{},
			ownerID:  realClusterID,
			expected: zoneUnlabeled,
		},
		{
			name:     "When the zone has labels but no ownership label, it should report unlabeled",
			zone:     &dns.ManagedZone{Labels: map[string]string{"goog-partner-solution": "openshift"}},
			ownerID:  realClusterID,
			expected: zoneUnlabeled,
		},
		{
			name:     "When the ownership label is empty, it should report unlabeled",
			zone:     &dns.ManagedZone{Labels: map[string]string{ownershipLabelKey: ""}},
			ownerID:  realClusterID,
			expected: zoneUnlabeled,
		},
		{
			name:     "When the zone is nil, it should report unlabeled",
			zone:     nil,
			ownerID:  realClusterID,
			expected: zoneUnlabeled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, classifyZoneOwnership(tt.zone, tt.ownerID))
		})
	}
}

func TestVerifyZoneOwnership(t *testing.T) {
	tests := []struct {
		name        string
		zone        *dns.ManagedZone
		expectedErr error
	}{
		{
			name: "When the zone is owned by this cluster, it should return no error",
			zone: &dns.ManagedZone{Name: "my-cluster-hypershift-local", Labels: map[string]string{ownershipLabelKey: realClusterID}},
		},
		{
			name:        "When the zone is owned by another cluster, it should return errZoneOwnedByOther",
			zone:        &dns.ManagedZone{Name: "my-cluster-hypershift-local", Labels: map[string]string{ownershipLabelKey: "8c7d6e5f-4a3b-4c2d-9e1f-0a1b2c3d4e5f"}},
			expectedErr: errZoneOwnedByOther,
		},
		{
			name:        "When the zone has no ownership label, it should return errZoneUnowned",
			zone:        &dns.ManagedZone{Name: "my-cluster-hypershift-local"},
			expectedErr: errZoneUnowned,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyZoneOwnership(tt.zone, realClusterID)
			if tt.expectedErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestZoneLabels(t *testing.T) {
	tests := []struct {
		name           string
		resourceLabels map[string]string
		expected       map[string]string
	}{
		{
			name:           "When no resource labels are configured, it should apply only the ownership label",
			resourceLabels: nil,
			expected:       map[string]string{ownershipLabelKey: realClusterID},
		},
		{
			name:           "When resource labels are configured, it should apply them alongside the ownership label",
			resourceLabels: map[string]string{"goog-partner-solution": "openshift", "environment": "prod"},
			expected: map[string]string{
				"goog-partner-solution": "openshift",
				"environment":           "prod",
				ownershipLabelKey:       realClusterID,
			},
		},
		{
			name:           "When a resource label would shadow the ownership label, it should keep the ownership value",
			resourceLabels: map[string]string{ownershipLabelKey: "8c7d6e5f-4a3b-4c2d-9e1f-0a1b2c3d4e5f"},
			expected:       map[string]string{ownershipLabelKey: realClusterID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, zoneLabels(tt.resourceLabels, realClusterID))
		})
	}
}

func TestMissingZoneLabels(t *testing.T) {
	desired := map[string]string{"goog-partner-solution": "openshift", ownershipLabelKey: realClusterID}

	tests := []struct {
		name     string
		zone     *dns.ManagedZone
		expected map[string]string
	}{
		{
			name:     "When the zone already carries every desired label, it should report no drift",
			zone:     &dns.ManagedZone{Labels: map[string]string{"goog-partner-solution": "openshift", ownershipLabelKey: realClusterID}},
			expected: nil,
		},
		{
			name:     "When the zone carries unrelated labels as well, it should still report no drift",
			zone:     &dns.ManagedZone{Labels: map[string]string{"goog-partner-solution": "openshift", ownershipLabelKey: realClusterID, "customer-label": "keep"}},
			expected: nil,
		},
		{
			name:     "When a desired label is missing, it should report that label",
			zone:     &dns.ManagedZone{Labels: map[string]string{ownershipLabelKey: realClusterID}},
			expected: map[string]string{"goog-partner-solution": "openshift"},
		},
		{
			name:     "When a desired label has a different value, it should report that label",
			zone:     &dns.ManagedZone{Labels: map[string]string{"goog-partner-solution": "stale", ownershipLabelKey: realClusterID}},
			expected: map[string]string{"goog-partner-solution": "openshift"},
		},
		{
			name:     "When the zone has no labels, it should report every desired label",
			zone:     &dns.ManagedZone{},
			expected: desired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, missingZoneLabels(tt.zone, desired))
		})
	}
}

func TestMergedZoneLabels(t *testing.T) {
	tests := []struct {
		name     string
		zone     *dns.ManagedZone
		desired  map[string]string
		expected map[string]string
	}{
		{
			name:     "When the zone carries unrelated labels, it should preserve them",
			zone:     &dns.ManagedZone{Labels: map[string]string{"customer-label": "keep"}},
			desired:  map[string]string{ownershipLabelKey: realClusterID},
			expected: map[string]string{"customer-label": "keep", ownershipLabelKey: realClusterID},
		},
		{
			name:     "When a desired label is stale on the zone, it should take the desired value",
			zone:     &dns.ManagedZone{Labels: map[string]string{"goog-partner-solution": "stale"}},
			desired:  map[string]string{"goog-partner-solution": "openshift"},
			expected: map[string]string{"goog-partner-solution": "openshift"},
		},
		{
			name:     "When the zone has no labels, it should return the desired labels",
			zone:     &dns.ManagedZone{},
			desired:  map[string]string{ownershipLabelKey: realClusterID},
			expected: map[string]string{ownershipLabelKey: realClusterID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, mergedZoneLabels(tt.zone, tt.desired))
		})
	}
}
