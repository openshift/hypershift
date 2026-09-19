package core

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	. "github.com/onsi/gomega"

	"k8s.io/utils/ptr"
)

func TestParseEtcdShards(t *testing.T) {
	testCases := []struct {
		name        string
		input       []string
		expected    []hyperv1.ManagedEtcdShardSpec
		expectedErr string
	}{
		{
			name:     "no flags yields no shards",
			input:    nil,
			expected: nil,
		},
		{
			name:  "minimal shard defaults to 3 replicas and inherited storage",
			input: []string{"name=events,resources=/events"},
			expected: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 3,
				Resources: []hyperv1.EtcdShardResource{
					{APIGroup: ptr.To(""), Resource: "events"},
				},
			}},
		},
		{
			name:  "all fields with multiple resources",
			input: []string{"name=events,resources=/events;events.k8s.io/events,replicas=1,storage=EmptyDir"},
			expected: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "events",
				Replicas: 1,
				Resources: []hyperv1.EtcdShardResource{
					{APIGroup: ptr.To(""), Resource: "events"},
					{APIGroup: ptr.To("events.k8s.io"), Resource: "events"},
				},
				Storage: hyperv1.ManagedEtcdShardStorageSpec{
					Type: hyperv1.EmptyDirEtcdShardStorage,
				},
			}},
		},
		{
			name:  "storageClassName implies PersistentVolume storage",
			input: []string{"name=leases,resources=coordination.k8s.io/leases,storageClassName=gp3-csi"},
			expected: []hyperv1.ManagedEtcdShardSpec{{
				Name:     "leases",
				Replicas: 3,
				Resources: []hyperv1.EtcdShardResource{
					{APIGroup: ptr.To("coordination.k8s.io"), Resource: "leases"},
				},
				Storage: hyperv1.ManagedEtcdShardStorageSpec{
					Type: hyperv1.PersistentVolumeEtcdShardStorage,
					PersistentVolume: hyperv1.ManagedEtcdShardPersistentVolumeSpec{
						StorageClassName: "gp3-csi",
					},
				},
			}},
		},
		{
			name: "multiple shards",
			input: []string{
				"name=events,resources=/events,storage=EmptyDir",
				"name=leases,resources=coordination.k8s.io/leases",
			},
			expected: []hyperv1.ManagedEtcdShardSpec{
				{
					Name:      "events",
					Replicas:  3,
					Resources: []hyperv1.EtcdShardResource{{APIGroup: ptr.To(""), Resource: "events"}},
					Storage:   hyperv1.ManagedEtcdShardStorageSpec{Type: hyperv1.EmptyDirEtcdShardStorage},
				},
				{
					Name:      "leases",
					Replicas:  3,
					Resources: []hyperv1.EtcdShardResource{{APIGroup: ptr.To("coordination.k8s.io"), Resource: "leases"}},
				},
			},
		},
		{
			name:        "missing name",
			input:       []string{"resources=/events"},
			expectedErr: "name is required",
		},
		{
			name:        "missing resources",
			input:       []string{"name=events"},
			expectedErr: "resources is required",
		},
		{
			name:        "field without equals",
			input:       []string{"name=events,resources"},
			expectedErr: `field "resources" is not in key=value form`,
		},
		{
			name:        "unknown field",
			input:       []string{"name=events,resources=/events,size=8Gi"},
			expectedErr: `unknown field "size"`,
		},
		{
			name:        "invalid replicas value",
			input:       []string{"name=events,resources=/events,replicas=2"},
			expectedErr: "replicas must be 1 or 3, got 2",
		},
		{
			name:        "non numeric replicas",
			input:       []string{"name=events,resources=/events,replicas=many"},
			expectedErr: `replicas "many" is not a number`,
		},
		{
			name:        "invalid storage type",
			input:       []string{"name=events,resources=/events,storage=Ephemeral"},
			expectedErr: "storage must be PersistentVolume or EmptyDir",
		},
		{
			name:        "storageClassName with EmptyDir",
			input:       []string{"name=events,resources=/events,storage=EmptyDir,storageClassName=gp3-csi"},
			expectedErr: "storageClassName is only valid when storage is PersistentVolume",
		},
		{
			name:        "resource without slash",
			input:       []string{"name=events,resources=events"},
			expectedErr: "must be in <apiGroup>/<resource> form",
		},
		{
			name:        "resource with empty resource name",
			input:       []string{"name=events,resources=events.k8s.io/"},
			expectedErr: "must specify a resource name after the slash",
		},
		{
			name:        "resource with too many slashes",
			input:       []string{"name=events,resources=events.k8s.io/v1/events"},
			expectedErr: "must contain exactly one slash",
		},
		{
			name: "duplicate shard name",
			input: []string{
				"name=events,resources=/events",
				"name=events,resources=coordination.k8s.io/leases",
			},
			expectedErr: `duplicate shard name "events"`,
		},
		{
			name: "overlapping resources across shards",
			input: []string{
				"name=events,resources=/events",
				"name=other,resources=/events",
			},
			expectedErr: `resource "/events" is already routed to shard "events"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			actual, err := parseEtcdShards(tc.input)
			if tc.expectedErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErr))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(actual).To(Equal(tc.expected))
		})
	}
}
