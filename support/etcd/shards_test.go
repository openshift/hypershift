package etcd

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/utils/ptr"
)

func TestEffectiveShards(t *testing.T) {
	tests := []struct {
		name           string
		managed        *hyperv1.ManagedEtcdSpec
		wantLen        int
		wantDefault    bool
		wantShardNames []string
	}{
		{
			name:    "nil managed returns nil",
			managed: nil,
			wantLen: 0,
		},
		{
			name: "no shards returns only default",
			managed: &hyperv1.ManagedEtcdSpec{
				Storage: hyperv1.ManagedEtcdStorageSpec{
					Type: hyperv1.PersistentVolumeEtcdStorage,
				},
			},
			wantLen:        1,
			wantDefault:    true,
			wantShardNames: []string{"etcd"},
		},
		{
			name: "with shards returns default plus shards",
			managed: &hyperv1.ManagedEtcdSpec{
				Storage: hyperv1.ManagedEtcdStorageSpec{
					Type: hyperv1.PersistentVolumeEtcdStorage,
				},
				Shards: []hyperv1.ManagedEtcdShardSpec{
					{
						Name: "events",
						Resources: []hyperv1.EtcdShardResource{
							{Resource: "events"},
						},
						Replicas: 1,
					},
					{
						Name: "leases",
						Resources: []hyperv1.EtcdShardResource{
							{APIGroup: ptr.To("coordination.k8s.io"), Resource: "leases"},
						},
						Replicas: 3,
					},
				},
			},
			wantLen:        3,
			wantDefault:    true,
			wantShardNames: []string{"etcd", "etcd-events", "etcd-leases"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shards := EffectiveShards(tt.managed)
			if len(shards) != tt.wantLen {
				t.Fatalf("got %d shards, want %d", len(shards), tt.wantLen)
			}
			if tt.wantLen == 0 {
				return
			}
			if shards[0].IsDefault != tt.wantDefault {
				t.Errorf("first shard IsDefault=%v, want %v", shards[0].IsDefault, tt.wantDefault)
			}
			for i, name := range tt.wantShardNames {
				if shards[i].Name != name {
					t.Errorf("shard[%d].Name=%q, want %q", i, shards[i].Name, name)
				}
			}
		})
	}
}

func TestUnmanagedEffectiveShards(t *testing.T) {
	tests := []struct {
		name      string
		unmanaged *hyperv1.UnmanagedEtcdSpec
		wantLen   int
	}{
		{
			name:      "nil returns nil",
			unmanaged: nil,
			wantLen:   0,
		},
		{
			name: "no shards returns only default",
			unmanaged: &hyperv1.UnmanagedEtcdSpec{
				Endpoint: "https://etcd:2379",
				TLS:      hyperv1.EtcdTLSConfig{},
			},
			wantLen: 1,
		},
		{
			name: "with shards",
			unmanaged: &hyperv1.UnmanagedEtcdSpec{
				Endpoint: "https://etcd:2379",
				TLS:      hyperv1.EtcdTLSConfig{},
				Shards: []hyperv1.UnmanagedEtcdShardSpec{
					{
						Name:     "events",
						Endpoint: "https://etcd-events:2379",
						Resources: []hyperv1.EtcdShardResource{
							{Resource: "events"},
						},
					},
				},
			},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shards := UnmanagedEffectiveShards(tt.unmanaged)
			if len(shards) != tt.wantLen {
				t.Fatalf("got %d shards, want %d", len(shards), tt.wantLen)
			}
		})
	}
}

func TestResourcePrefix(t *testing.T) {
	tests := []struct {
		name     string
		resource hyperv1.EtcdShardResource
		want     string
	}{
		{
			name:     "core group",
			resource: hyperv1.EtcdShardResource{Resource: "events"},
			want:     "/events",
		},
		{
			name:     "empty string apiGroup",
			resource: hyperv1.EtcdShardResource{APIGroup: ptr.To(""), Resource: "events"},
			want:     "/events",
		},
		{
			name:     "non-core group",
			resource: hyperv1.EtcdShardResource{APIGroup: ptr.To("coordination.k8s.io"), Resource: "leases"},
			want:     "coordination.k8s.io/leases",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resourcePrefix(tt.resource)
			if got != tt.want {
				t.Errorf("resourcePrefix()=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientServiceName(t *testing.T) {
	tests := []struct {
		shardName string
		want      string
	}{
		{"etcd", "etcd-client"},
		{"etcd-events", "etcd-client-events"},
		{"etcd-leases", "etcd-client-leases"},
	}
	for _, tt := range tests {
		got := ClientServiceName(tt.shardName)
		if got != tt.want {
			t.Errorf("ClientServiceName(%q)=%q, want %q", tt.shardName, got, tt.want)
		}
	}
}

func TestDiscoveryServiceName(t *testing.T) {
	tests := []struct {
		shardName string
		want      string
	}{
		{"etcd", "etcd-discovery"},
		{"etcd-events", "etcd-discovery-events"},
		{"etcd-leases", "etcd-discovery-leases"},
	}
	for _, tt := range tests {
		got := DiscoveryServiceName(tt.shardName)
		if got != tt.want {
			t.Errorf("DiscoveryServiceName(%q)=%q, want %q", tt.shardName, got, tt.want)
		}
	}
}
