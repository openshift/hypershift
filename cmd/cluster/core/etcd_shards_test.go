package core

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

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
			name:     "When no --etcd-shard flags are given, it should return no shards",
			input:    nil,
			expected: nil,
		},
		{
			name:  "When only name and resources are given, it should default to 3 replicas and inherited storage",
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
			name:  "When all fields and multiple resources are given, it should parse every field",
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
			name:  "When storageClassName is given without storage, it should imply PersistentVolume storage",
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
			name: "When multiple --etcd-shard flags are given, it should parse one shard per flag",
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
			name:        "When name is omitted, it should report that name is required",
			input:       []string{"resources=/events"},
			expectedErr: "name is required",
		},
		{
			name:        "When resources is omitted, it should report that resources is required",
			input:       []string{"name=events"},
			expectedErr: "resources is required",
		},
		{
			name:        "When a field is not in key=value form, it should report the malformed field",
			input:       []string{"name=events,resources"},
			expectedErr: `field "resources" is not in key=value form`,
		},
		{
			name:        "When an unknown field is given, it should report the unknown field",
			input:       []string{"name=events,resources=/events,size=8Gi"},
			expectedErr: `unknown field "size"`,
		},
		{
			name:        "When replicas is neither 1 nor 3, it should report the allowed values",
			input:       []string{"name=events,resources=/events,replicas=2"},
			expectedErr: "replicas must be 1 or 3, got 2",
		},
		{
			name:        "When replicas is not a number, it should report that it is not a number",
			input:       []string{"name=events,resources=/events,replicas=many"},
			expectedErr: `replicas "many" is not a number`,
		},
		{
			name:        "When storage is not a known type, it should report the allowed storage types",
			input:       []string{"name=events,resources=/events,storage=Ephemeral"},
			expectedErr: "storage must be PersistentVolume or EmptyDir",
		},
		{
			name:        "When storageClassName is combined with EmptyDir, it should report that it requires PersistentVolume",
			input:       []string{"name=events,resources=/events,storage=EmptyDir,storageClassName=gp3-csi"},
			expectedErr: "storageClassName is only valid when storage is PersistentVolume",
		},
		{
			name:        "When a resource has no slash, it should report the required <apiGroup>/<resource> form",
			input:       []string{"name=events,resources=events"},
			expectedErr: "must be in <apiGroup>/<resource> form",
		},
		{
			name:        "When a resource has nothing after the slash, it should report the missing resource name",
			input:       []string{"name=events,resources=events.k8s.io/"},
			expectedErr: "must specify a resource name after the slash",
		},
		{
			name:        "When a resource has more than one slash, it should report that exactly one slash is required",
			input:       []string{"name=events,resources=events.k8s.io/v1/events"},
			expectedErr: "must contain exactly one slash",
		},
		{
			name: "When two shards share a name, it should report the duplicate shard name",
			input: []string{
				"name=events,resources=/events",
				"name=events,resources=coordination.k8s.io/leases",
			},
			expectedErr: `duplicate shard name "events"`,
		},
		{
			name: "When two shards claim the same resource, it should report the already-routed resource",
			input: []string{
				"name=events,resources=/events",
				"name=other,resources=/events",
			},
			expectedErr: `resource "/events" is already routed to shard "events"`,
		},
		{
			name:        "When a field is specified more than once, it should reject it instead of silently taking the last value",
			input:       []string{"name=events,name=leases,resources=/events"},
			expectedErr: `field "name" is specified more than once`,
		},
		{
			name:        "When storageClassName is not a valid DNS1123 subdomain, it should reject it before the API does",
			input:       []string{"name=leases,resources=/events,storageClassName=GP3_CSI"},
			expectedErr: `storageClassName "GP3_CSI" is not a valid DNS1123 subdomain`,
		},
		{
			name:        "When the shard name is not a valid DNS1123 label, it should reject it before the API does",
			input:       []string{"name=Events_Shard,resources=/events"},
			expectedErr: `name "Events_Shard" is not a valid DNS1123 label`,
		},
		{
			name:        "When the shard name exceeds the length limit, it should report the maximum",
			input:       []string{"name=" + strings.Repeat("a", 49) + ",resources=/events"},
			expectedErr: "must be at most 48 characters",
		},
		{
			name:        "When the apiGroup is not a valid DNS subdomain, it should reject it before the API does",
			input:       []string{"name=events,resources=Events.K8s.io/events"},
			expectedErr: `apiGroup "Events.K8s.io" is not a valid DNS subdomain`,
		},
		{
			name:        "When the resource name is not a valid DNS label, it should reject it before the API does",
			input:       []string{"name=events,resources=/Events"},
			expectedErr: `resource name "Events" is not a valid DNS label`,
		},
		{
			name:        "When a shard routes more than 20 resources, it should report the maximum",
			input:       []string{"name=events,resources=" + strings.Join(manyEtcdShardResources(21), ";")},
			expectedErr: "resources must contain at most 20 entries, got 21",
		},
		{
			name:  "When a shard routes exactly 20 resources, it should accept them",
			input: []string{"name=events,resources=" + strings.Join(manyEtcdShardResources(20), ";")},
			expected: []hyperv1.ManagedEtcdShardSpec{{
				Name:      "events",
				Replicas:  3,
				Resources: expectedEtcdShardResources(20),
			}},
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

// manyEtcdShardResources returns n distinct core-group resource entries in the
// "<apiGroup>/<resource>" form accepted by the --etcd-shard resources field.
func manyEtcdShardResources(n int) []string {
	entries := make([]string, 0, n)
	for i := range n {
		entries = append(entries, fmt.Sprintf("/resource-%d", i))
	}
	return entries
}

// expectedEtcdShardResources returns the parsed form of manyEtcdShardResources.
func expectedEtcdShardResources(n int) []hyperv1.EtcdShardResource {
	resources := make([]hyperv1.EtcdShardResource, 0, n)
	for i := range n {
		resources = append(resources, hyperv1.EtcdShardResource{
			APIGroup: ptr.To(""),
			Resource: fmt.Sprintf("resource-%d", i),
		})
	}
	return resources
}
